// Copyright 2018 The gVisor Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package localfs exposes the host's local file system as a p9.File.
package localfs

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/hugelgupf/p9/fsimpl/templatefs"
	"github.com/hugelgupf/p9/internal"
	"github.com/hugelgupf/p9/linux"
	"github.com/hugelgupf/p9/p9"
)

type attacher struct {
	root string
}

var (
	_ p9.Attacher = &attacher{}
)

// RootAttacher attaches at the host file system's root.
func RootAttacher() p9.Attacher {
	return &attacher{root: "/"}
}

// Attacher returns an attacher that exposes files under root.
func Attacher(root string) p9.Attacher {
	if len(root) == 0 {
		root = "/"
	}
	return &attacher{root: root}
}

// Attach implements p9.Attacher.Attach.
func (a *attacher) Attach() (p9.File, error) {
	umask(0)
	r, err := openRootDir(a.root)
	if err != nil {
		return nil, err
	}
	return &Local{root: r}, nil
}

// Local is a p9.File.
type Local struct {
	p9.DefaultWalkGetAttr
	templatefs.NoopFile

	root   *rootDir
	rel    string
	file   *os.File
	mode   p9.OpenFlags
	closed atomic.Bool
}

var (
	_ p9.File = &Local{}
)

func (l *Local) child(rel string, file *os.File, mode p9.OpenFlags) *Local {
	l.root.acquireFID()
	return &Local{
		root: l.root,
		rel:  rel,
		file: file,
		mode: mode,
	}
}

// validateName ensures name is a single safe path component: non-empty, not
// "." or "..", free of path separators and NUL bytes, and locally valid on the
// host OS (e.g. rejecting Windows drive prefixes, reserved device names, and
// trailing dots or spaces stripped by Win32 path normalization).
func validateName(name string) error {
	if name == "" || name == "." || name == ".." {
		return linux.EINVAL
	}
	if strings.ContainsRune(name, '/') || strings.ContainsRune(name, '\x00') {
		return linux.EINVAL
	}
	if os.PathSeparator != '/' && strings.ContainsRune(name, os.PathSeparator) {
		return linux.EINVAL
	}
	if runtime.GOOS == "windows" {
		if last := name[len(name)-1]; last == '.' || last == ' ' {
			return linux.EINVAL
		}
	}
	if !filepath.IsLocal(name) {
		return linux.EINVAL
	}
	return nil
}

// validateRel ensures every slash-separated component in rel is a safe child
// name. An empty rel denotes the attached export root itself.
func validateRel(rel string) error {
	if rel == "" {
		return nil
	}
	for comp := range strings.SplitSeq(rel, "/") {
		if err := validateName(comp); err != nil {
			return err
		}
	}
	return nil
}

func joinRel(base, elem string) string {
	if base == "" {
		return elem
	}
	return base + "/" + elem
}

func splitRel(rel string) (dir, base string) {
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		return rel[:i], rel[i+1:]
	}
	return "", rel
}

// filterOpenFlags translates host os.O_* flags carried in p9.OpenFlags via an
// explicit allowlist so callers cannot smuggle unsafe or cross-OS-colliding
// flags (such as O_PATH, O_SYMLINK, O_TMPFILE, O_CREAT, O_EXCL, O_DIRECTORY,
// or Win32 FILE_FLAG_* bits).
func filterOpenFlags(mode p9.OpenFlags, allowed int) (int, error) {
	var flags int
	switch mode.Mode() {
	case p9.ReadOnly:
		flags = os.O_RDONLY
	case p9.WriteOnly:
		flags = os.O_WRONLY
	case p9.ReadWrite:
		flags = os.O_RDWR
	default:
		return 0, linux.EINVAL
	}
	return flags | (int(mode) & allowed), nil
}

func resolveTimes(valid p9.SetAttrMask, attr p9.SetAttr) (atime, mtime time.Time) {
	now := time.Now()
	if valid.ATime {
		atime = now
		if valid.ATimeNotSystemTime {
			atime = time.Unix(int64(attr.ATimeSeconds), int64(attr.ATimeNanoSeconds))
		}
	}
	if valid.MTime {
		mtime = now
		if valid.MTimeNotSystemTime {
			mtime = time.Unix(int64(attr.MTimeSeconds), int64(attr.MTimeNanoSeconds))
		}
	}
	return atime, mtime
}

func statToAttr(st *internal.Stat_t) p9.Attr {
	return p9.Attr{
		Mode:             p9.FileMode(st.Mode),
		UID:              p9.UID(st.Uid),
		GID:              p9.GID(st.Gid),
		NLink:            p9.NLink(st.Nlink),
		RDev:             p9.Dev(st.Rdev),
		Size:             uint64(st.Size),
		BlockSize:        uint64(st.Blksize),
		Blocks:           uint64(st.Blocks),
		ATimeSeconds:     uint64(st.Atim.Sec),
		ATimeNanoSeconds: uint64(st.Atim.Nsec),
		MTimeSeconds:     uint64(st.Mtim.Sec),
		MTimeNanoSeconds: uint64(st.Mtim.Nsec),
		CTimeSeconds:     uint64(st.Ctim.Sec),
		CTimeNanoSeconds: uint64(st.Ctim.Nsec),
	}
}

// Walk implements p9.File.Walk.
func (l *Local) Walk(names []string) ([]p9.QID, p9.File, error) {
	// A walk with no names is a copy of self.
	if len(names) == 0 {
		if l.root == nil {
			return nil, nil, linux.EBADF
		}
		if err := validateRel(l.rel); err != nil {
			return nil, nil, err
		}
		return nil, l.child(l.rel, nil, 0), nil
	}

	for _, name := range names {
		if err := validateName(name); err != nil {
			return nil, nil, err
		}
	}
	return l.walk(names)
}

// FSync implements p9.File.FSync.
func (l *Local) FSync() error {
	return l.file.Sync()
}

// GetAttr implements p9.File.GetAttr.
//
// Not fully implemented.
func (l *Local) GetAttr(req p9.AttrMask) (p9.QID, p9.AttrMask, p9.Attr, error) {
	return l.getAttr(req)
}

// Close implements p9.File.Close.
func (l *Local) Close() error {
	var err error
	if l.file != nil {
		// We don't set l.file = nil, as Close is called by servers
		// only in Clunk. Clunk should release the last (direct)
		// reference to this file.
		err = l.file.Close()
	}
	if l.root != nil && l.closed.CompareAndSwap(false, true) {
		l.root.releaseFID()
	}
	return err
}

// Open implements p9.File.Open.
func (l *Local) Open(mode p9.OpenFlags) (p9.QID, uint32, error) {
	return l.open(mode)
}

// ReadAt implements p9.File.ReadAt.
func (l *Local) ReadAt(p []byte, offset int64) (int, error) {
	return l.file.ReadAt(p, offset)
}

// Lock implements p9.File.Lock.
func (l *Local) Lock(pid int, locktype p9.LockType, flags p9.LockFlags, start, length uint64, client string) (p9.LockStatus, error) {
	return l.lock(pid, locktype, flags, start, length, client)
}

// WriteAt implements p9.File.WriteAt.
func (l *Local) WriteAt(p []byte, offset int64) (int, error) {
	// Go's (*os.File).WriteAt unconditionally returns an unexported error
	// when the file was opened with os.O_APPEND.
	if int(l.mode)&os.O_APPEND != 0 {
		return l.file.Write(p)
	}
	return l.file.WriteAt(p, offset)
}

// Create implements p9.File.Create.
func (l *Local) Create(name string, mode p9.OpenFlags, permissions p9.FileMode, _ p9.UID, _ p9.GID) (p9.File, p9.QID, uint32, error) {
	if err := validateName(name); err != nil {
		return nil, p9.QID{}, 0, err
	}
	return l.create(name, mode, permissions)
}

// Mkdir implements p9.File.Mkdir.
//
// Not properly implemented.
func (l *Local) Mkdir(name string, permissions p9.FileMode, _ p9.UID, _ p9.GID) (p9.QID, error) {
	if err := validateName(name); err != nil {
		return p9.QID{}, err
	}
	if err := l.mkdir(name, permissions); err != nil {
		return p9.QID{}, err
	}

	// Blank QID.
	return p9.QID{}, nil
}

// Symlink implements p9.File.Symlink.
//
// Not properly implemented.
func (l *Local) Symlink(oldname string, newname string, _ p9.UID, _ p9.GID) (p9.QID, error) {
	if err := validateName(newname); err != nil {
		return p9.QID{}, err
	}
	if err := l.symlink(oldname, newname); err != nil {
		return p9.QID{}, err
	}

	// Blank QID.
	return p9.QID{}, nil
}

func (l *Local) sameRootLocal(other p9.File) (*Local, error) {
	if other == nil {
		return nil, linux.EBADF
	}
	o, ok := other.(*Local)
	if !ok {
		return nil, linux.EXDEV
	}
	if l.root == nil || o == nil || o.root == nil {
		return nil, linux.EBADF
	}
	if l.root.id != o.root.id {
		return nil, linux.EXDEV
	}
	return o, nil
}

// Link implements p9.File.Link.
//
// Not properly implemented.
func (l *Local) Link(target p9.File, newname string) error {
	if err := validateName(newname); err != nil {
		return err
	}
	t, err := l.sameRootLocal(target)
	if err != nil {
		return err
	}
	return l.link(t, newname)
}

// RenameAt implements p9.File.RenameAt.
func (l *Local) RenameAt(oldName string, newDir p9.File, newName string) error {
	if err := validateName(oldName); err != nil {
		return err
	}
	if err := validateName(newName); err != nil {
		return err
	}
	nd, err := l.sameRootLocal(newDir)
	if err != nil {
		return err
	}
	return l.renameAt(oldName, nd, newName)
}

// Readlink implements p9.File.Readlink.
//
// Not properly implemented.
func (l *Local) Readlink() (string, error) {
	return l.readlink()
}

// Renamed implements p9.File.Renamed.
func (l *Local) Renamed(parent p9.File, newName string) {
	p, err := l.sameRootLocal(parent)
	if err != nil || validateName(newName) != nil {
		// Fail closed if a direct caller passes an unsafe name,
		// foreign file, or mismatched root.
		l.rel = ".."
		return
	}
	l.rel = joinRel(p.rel, newName)
}

// SetAttr implements p9.File.SetAttr.
func (l *Local) SetAttr(valid p9.SetAttrMask, attr p9.SetAttr) error {
	// CTime cannot be set, but Linux sends it along with other fields --
	// e.g. when truncate(2) is called -- so accept and ignore it.
	supported := p9.SetAttrMask{
		Permissions:        true,
		UID:                true,
		GID:                true,
		Size:               true,
		MTime:              true,
		CTime:              true,
		ATime:              true,
		ATimeNotSystemTime: true,
		MTimeNotSystemTime: true,
	}
	if !valid.IsSubsetOf(supported) {
		return linux.ENOSYS
	}
	if l.root == nil {
		return linux.EBADF
	}
	if err := validateRel(l.rel); err != nil {
		return err
	}
	if !valid.Size && !valid.ATime && !valid.MTime && !valid.UID && !valid.GID && !valid.Permissions {
		return nil
	}
	return l.setAttr(valid, attr)
}

// Remove implements p9.File.Remove.
func (l *Local) Remove() error {
	return l.remove()
}

// UnlinkAt implements p9.File.UnlinkAt.
func (l *Local) UnlinkAt(name string, flags uint32) error {
	if err := validateName(name); err != nil {
		return err
	}
	return l.unlinkAt(name, flags)
}

// Readdir implements p9.File.Readdir.
func (l *Local) Readdir(offset uint64, count uint32) (p9.Dirents, error) {
	return l.readDir(offset, count)
}
