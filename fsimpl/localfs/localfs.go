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
	"path"
	"path/filepath"
	"runtime"
	"strings"
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
	return &Local{path: a.root}, nil
}

// Local is a p9.File.
type Local struct {
	p9.DefaultWalkGetAttr
	templatefs.NoopFile

	path string
	file *os.File
	mode p9.OpenFlags
}

var (
	_ p9.File = &Local{}
)

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

// info constructs a QID for this file.
func (l *Local) info() (p9.QID, os.FileInfo, error) {
	var (
		qid p9.QID
		fi  os.FileInfo
		err error
	)

	// Stat the file.
	if l.file != nil {
		fi, err = l.file.Stat()
	} else {
		if l.path == "" {
			return qid, nil, linux.EINVAL
		}
		fi, err = os.Lstat(l.path)
	}
	if err != nil {
		return qid, nil, err
	}

	// Construct the QID type.
	qid.Type = p9.ModeFromOS(fi.Mode()).QIDType()

	// Save the path from the Ino.
	ninePath, err := localToQid(l.path, fi)
	if err != nil {
		return qid, nil, err
	}

	qid.Path = ninePath

	return qid, fi, nil
}

// Walk implements p9.File.Walk.
func (l *Local) Walk(names []string) ([]p9.QID, p9.File, error) {
	var qids []p9.QID
	last := &Local{path: l.path}

	// A walk with no names is a copy of self.
	if len(names) == 0 {
		return nil, last, nil
	}
	if l.path == "" {
		return nil, nil, linux.EINVAL
	}

	for _, name := range names {
		if err := validateName(name); err != nil {
			return nil, nil, err
		}
		c := &Local{path: path.Join(last.path, name)}
		qid, _, err := c.info()
		if err != nil {
			return nil, nil, err
		}
		qids = append(qids, qid)
		last = c
	}
	return qids, last, nil
}

// FSync implements p9.File.FSync.
func (l *Local) FSync() error {
	return l.file.Sync()
}

// GetAttr implements p9.File.GetAttr.
//
// Not fully implemented.
func (l *Local) GetAttr(req p9.AttrMask) (p9.QID, p9.AttrMask, p9.Attr, error) {
	qid, fi, err := l.info()
	if err != nil {
		return qid, p9.AttrMask{}, p9.Attr{}, err
	}

	stat := internal.InfoToStat(fi)
	attr := &p9.Attr{
		Mode:             p9.FileMode(stat.Mode),
		UID:              p9.UID(stat.Uid),
		GID:              p9.GID(stat.Gid),
		NLink:            p9.NLink(stat.Nlink),
		RDev:             p9.Dev(stat.Rdev),
		Size:             uint64(stat.Size),
		BlockSize:        uint64(stat.Blksize),
		Blocks:           uint64(stat.Blocks),
		ATimeSeconds:     uint64(stat.Atim.Sec),
		ATimeNanoSeconds: uint64(stat.Atim.Nsec),
		MTimeSeconds:     uint64(stat.Mtim.Sec),
		MTimeNanoSeconds: uint64(stat.Mtim.Nsec),
		CTimeSeconds:     uint64(stat.Ctim.Sec),
		CTimeNanoSeconds: uint64(stat.Ctim.Nsec),
	}
	return qid, req, *attr, nil
}

// Close implements p9.File.Close.
func (l *Local) Close() error {
	if l.file != nil {
		// We don't set l.file = nil, as Close is called by servers
		// only in Clunk. Clunk should release the last (direct)
		// reference to this file.
		return l.file.Close()
	}
	return nil
}

// Open implements p9.File.Open.
func (l *Local) Open(mode p9.OpenFlags) (p9.QID, uint32, error) {
	qid, _, err := l.info()
	if err != nil {
		return qid, 0, err
	}

	// Do the actual open.
	f, err := os.OpenFile(l.path, int(mode), 0)
	if err != nil {
		return qid, 0, err
	}
	l.file = f
	l.mode = mode

	return qid, 0, nil
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
	if l.path == "" {
		return nil, p9.QID{}, 0, linux.EINVAL
	}
	if err := validateName(name); err != nil {
		return nil, p9.QID{}, 0, err
	}
	newName := path.Join(l.path, name)
	f, err := os.OpenFile(newName, int(mode)|os.O_CREATE|os.O_EXCL, os.FileMode(permissions))
	if err != nil {
		return nil, p9.QID{}, 0, err
	}

	l2 := &Local{path: newName, file: f, mode: mode}
	qid, _, err := l2.info()
	if err != nil {
		l2.Close()
		return nil, p9.QID{}, 0, err
	}
	return l2, qid, 0, nil
}

// Mkdir implements p9.File.Mkdir.
//
// Not properly implemented.
func (l *Local) Mkdir(name string, permissions p9.FileMode, _ p9.UID, _ p9.GID) (p9.QID, error) {
	if l.path == "" {
		return p9.QID{}, linux.EINVAL
	}
	if err := validateName(name); err != nil {
		return p9.QID{}, err
	}
	if err := os.Mkdir(path.Join(l.path, name), os.FileMode(permissions)); err != nil {
		return p9.QID{}, err
	}

	// Blank QID.
	return p9.QID{}, nil
}

// Symlink implements p9.File.Symlink.
//
// Not properly implemented.
func (l *Local) Symlink(oldname string, newname string, _ p9.UID, _ p9.GID) (p9.QID, error) {
	if l.path == "" {
		return p9.QID{}, linux.EINVAL
	}
	if err := validateName(newname); err != nil {
		return p9.QID{}, err
	}
	if err := os.Symlink(oldname, path.Join(l.path, newname)); err != nil {
		return p9.QID{}, err
	}

	// Blank QID.
	return p9.QID{}, nil
}

// Link implements p9.File.Link.
//
// Not properly implemented.
func (l *Local) Link(target p9.File, newname string) error {
	if l.path == "" {
		return linux.EINVAL
	}
	if err := validateName(newname); err != nil {
		return err
	}
	t := target.(*Local)
	if t.path == "" {
		return linux.EINVAL
	}
	return os.Link(t.path, path.Join(l.path, newname))
}

// RenameAt implements p9.File.RenameAt.
func (l *Local) RenameAt(oldName string, newDir p9.File, newName string) error {
	if l.path == "" {
		return linux.EINVAL
	}
	if err := validateName(oldName); err != nil {
		return err
	}
	if err := validateName(newName); err != nil {
		return err
	}
	nd := newDir.(*Local)
	if nd.path == "" {
		return linux.EINVAL
	}
	oldPath := path.Join(l.path, oldName)
	newPath := path.Join(nd.path, newName)

	return os.Rename(oldPath, newPath)
}

// Readlink implements p9.File.Readlink.
//
// Not properly implemented.
func (l *Local) Readlink() (string, error) {
	if l.path == "" {
		return "", linux.EINVAL
	}
	return os.Readlink(l.path)
}

// Renamed implements p9.File.Renamed.
func (l *Local) Renamed(parent p9.File, newName string) {
	p := parent.(*Local)
	if p.path == "" || validateName(newName) != nil {
		// Fail closed if a direct caller passes an unsafe name.
		l.path = ""
		return
	}
	l.path = path.Join(p.path, newName)
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
	if l.path == "" {
		return linux.EINVAL
	}

	// Perform Size, ATime/MTime, and UID/GID before Permissions so that:
	// 1. A single SetAttr call that both modifies size/timestamps and
	//    removes write permissions (e.g., chmod 0) does not fail its own
	//    truncate or chtimes.
	// 2. Chmod runs last so setuid/setgid bits are not cleared by truncate
	//    or chown.
	if valid.Size {
		if err := os.Truncate(l.path, int64(attr.Size)); err != nil {
			return err
		}
	}
	if valid.ATime || valid.MTime {
		// Chtimes leaves a time unchanged if it is the zero time.
		var atime, mtime time.Time
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
		if err := os.Chtimes(l.path, atime, mtime); err != nil {
			return err
		}
	}
	if valid.UID || valid.GID {
		uid, gid := -1, -1
		if valid.UID {
			uid = int(attr.UID)
		}
		if valid.GID {
			gid = int(attr.GID)
		}
		if err := os.Chown(l.path, uid, gid); err != nil {
			return err
		}
	}
	if valid.Permissions {
		// OSMode moves the setuid, setgid and sticky bits into the
		// positions os.Chmod expects.
		if err := os.Chmod(l.path, attr.Permissions.Permissions().OSMode()); err != nil {
			return err
		}
	}
	return nil
}

// Remove implements p9.File.Remove.
func (l *Local) Remove() error {
	if l.path == "" {
		return linux.EINVAL
	}
	return os.Remove(l.path)
}

// UnlinkAt implements p9.File.UnlinkAt
func (l *Local) UnlinkAt(name string, flags uint32) error {
	if l.path == "" {
		return linux.EINVAL
	}
	if err := validateName(name); err != nil {
		return err
	}
	// Construct the full path
	fullPath := filepath.Join(l.path, name)

	// Remove the file or directory
	return os.Remove(fullPath)
}
