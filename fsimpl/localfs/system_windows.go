package localfs

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unsafe"

	"github.com/hugelgupf/p9/internal"
	"github.com/hugelgupf/p9/linux"
	"github.com/hugelgupf/p9/p9"
	"golang.org/x/sys/windows"
)

func umask(_ int) int {
	return 0
}

type winFileID struct {
	volSerial uint32
	fileIndex uint64
}

// rootDir records the export root's path and pinned (volume serial, file
// index) identity on Windows without holding an open directory handle across
// Attach's lifetime. Holding an open os.Root handle (which omits
// FILE_SHARE_DELETE on Windows) would prevent callers and tests that do not
// close every root fid from removing the directory.
type rootDir struct {
	path string
	id   winFileID
}

func withFileHandle(f *os.File, fn func(windows.Handle) error) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var fnErr error
	if err := rc.Control(func(fd uintptr) {
		fnErr = fn(windows.Handle(fd))
	}); err != nil {
		return err
	}
	return fnErr
}

func fileIdentityByHandle(h windows.Handle) (winFileID, error) {
	var fi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &fi); err != nil {
		return winFileID{}, err
	}
	return winFileID{
		volSerial: fi.VolumeSerialNumber,
		fileIndex: uint64(fi.FileIndexHigh)<<32 | uint64(fi.FileIndexLow),
	}, nil
}

func fileIdentity(f *os.File) (winFileID, error) {
	var id winFileID
	err := withFileHandle(f, func(h windows.Handle) error {
		var hErr error
		id, hErr = fileIdentityByHandle(h)
		return hErr
	})
	return id, err
}

func rootIdentity(r *os.Root) (winFileID, error) {
	f, err := r.Open(".")
	if err != nil {
		return winFileID{}, err
	}
	defer f.Close()
	return fileIdentity(f)
}

func openRootDir(path string) (*rootDir, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(abs)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	id, err := rootIdentity(r)
	if err != nil {
		return nil, err
	}
	return &rootDir{
		path: abs,
		id:   id,
	}, nil
}

func (r *rootDir) acquireFID() {}
func (r *rootDir) releaseFID() {}

func (r *rootDir) openRoot() (*os.Root, error) {
	if r == nil {
		return nil, linux.EBADF
	}
	root, err := os.OpenRoot(r.path)
	if err != nil {
		return nil, err
	}
	id, err := rootIdentity(root)
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	if id != r.id {
		_ = root.Close()
		return nil, linux.ESTALE
	}
	return root, nil
}

func relOrDot(rel string) string {
	if rel == "" {
		return "."
	}
	return filepath.FromSlash(rel)
}

const windowsAllowedOpenFlags = os.O_TRUNC | os.O_APPEND | os.O_SYNC

type pathCheck uint8

const (
	checkDir    pathCheck = iota // all components must be real dirs
	checkParent                  // parent components must be real dirs
	checkTarget                  // parents dirs; leaf not link/junction
)

// checkPathInRoot verifies rel component-by-component inside r using r.Lstat
// so neither intermediate nor (per mode) leaf symlinks or directory junctions
// are followed, even when their target stays within root.
func checkPathInRoot(r *os.Root, rel string, mode pathCheck) error {
	if rel == "" {
		return nil
	}
	if err := validateRel(rel); err != nil {
		return err
	}
	parts := strings.Split(rel, "/")
	limit := len(parts)
	if mode == checkParent {
		limit = len(parts) - 1
	}
	var p string
	for i := range limit {
		if i == 0 {
			p = parts[0]
		} else {
			p += string(os.PathSeparator) + parts[i]
		}
		fi, err := r.Lstat(p)
		if err != nil {
			return err
		}
		if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return linux.ELOOP
		}
		if i < len(parts)-1 || mode == checkDir {
			if fi.Mode()&os.ModeType != os.ModeDir {
				return linux.ENOTDIR
			}
		}
	}
	return nil
}

func openCheckedRootRel(root *rootDir, rel string, mode pathCheck) (*os.Root, error) {
	r, err := root.openRoot()
	if err != nil {
		return nil, err
	}
	if err := checkPathInRoot(r, rel, mode); err != nil {
		_ = r.Close()
		return nil, err
	}
	return r, nil
}

func (l *Local) openCheckedRoot(mode pathCheck) (*os.Root, error) {
	return openCheckedRootRel(l.root, l.rel, mode)
}

// fileIdentityAt reads the NTFS/ReFS (volume serial, file index) identity for
// a single child name relative to dirHandle without following symlinks or
// crossing MAX_PATH.
func fileIdentityAt(dirHandle windows.Handle, name string) (winFileID, error) {
	ntName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return winFileID{}, err
	}
	oa := &windows.OBJECT_ATTRIBUTES{
		RootDirectory: dirHandle,
		ObjectName:    ntName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
	}
	oa.Length = uint32(unsafe.Sizeof(*oa))

	const (
		access      = windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE
		share       = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE
		disposition = windows.FILE_OPEN
		options     = windows.FILE_OPEN_FOR_BACKUP_INTENT | windows.FILE_OPEN_REPARSE_POINT | windows.FILE_SYNCHRONOUS_IO_NONALERT
	)
	var (
		h    windows.Handle
		iosb windows.IO_STATUS_BLOCK
	)
	err = windows.NtCreateFile(&h, access, oa, &iosb, nil, 0, share, disposition, options, 0, 0)
	if err == windows.STATUS_INVALID_PARAMETER {
		// Older Windows builds (Win10 10240) reject OBJ_DONT_REPARSE;
		// name is a single component and FILE_OPEN_REPARSE_POINT still
		// prevents following it.
		oa.Attributes &^= windows.OBJ_DONT_REPARSE
		err = windows.NtCreateFile(&h, access, oa, &iosb, nil, 0, share, disposition, options, 0, 0)
	}
	if err != nil {
		if st, ok := err.(windows.NTStatus); ok {
			return winFileID{}, st.Errno()
		}
		return winFileID{}, err
	}
	defer windows.CloseHandle(h)

	return fileIdentityByHandle(h)
}

func identityInRoot(r *os.Root, rel string) (winFileID, error) {
	if rel == "" {
		return rootIdentity(r)
	}
	dir, base := splitRel(rel)
	dirFile, err := r.Open(relOrDot(dir))
	if err != nil {
		return winFileID{}, err
	}
	defer dirFile.Close()

	var id winFileID
	err = withFileHandle(dirFile, func(h windows.Handle) error {
		var hErr error
		id, hErr = fileIdentityAt(h, base)
		return hErr
	})
	return id, err
}

func verifySameFile(a, b *os.File) error {
	idA, err := fileIdentity(a)
	if err != nil {
		return err
	}
	idB, err := fileIdentity(b)
	if err != nil {
		return err
	}
	if idA != idB {
		return linux.ESTALE
	}
	return nil
}

func verifyRootTargetMatchesFile(r *os.Root, rel string, openFile *os.File) error {
	openID, err := fileIdentity(openFile)
	if err != nil {
		return err
	}
	targetID, err := identityInRoot(r, rel)
	if err != nil {
		return err
	}
	if openID != targetID {
		return linux.ESTALE
	}
	return nil
}

func makeQID(mode os.FileMode, fileIndex uint64) p9.QID {
	return p9.QID{
		Type: p9.ModeFromOS(mode).QIDType(),
		Path: fileIndex,
	}
}

func qidInRoot(r *os.Root, rel string, fi os.FileInfo) (p9.QID, error) {
	id, err := identityInRoot(r, rel)
	if err != nil {
		return p9.QID{}, err
	}
	return makeQID(fi.Mode(), id.fileIndex), nil
}

func qidFromOpenFile(f *os.File, fi os.FileInfo) (p9.QID, error) {
	id, err := fileIdentity(f)
	if err != nil {
		return p9.QID{}, err
	}
	return makeQID(fi.Mode(), id.fileIndex), nil
}

func openFileQID(f *os.File) (p9.QID, error) {
	fi, err := f.Stat()
	if err != nil {
		return p9.QID{}, err
	}
	return qidFromOpenFile(f, fi)
}

func (l *Local) getAttr(req p9.AttrMask) (p9.QID, p9.AttrMask, p9.Attr, error) {
	if h := l.open.Load(); h != nil {
		fi, err := h.file.Stat()
		if err != nil {
			return p9.QID{}, p9.AttrMask{}, p9.Attr{}, err
		}
		qid, err := qidFromOpenFile(h.file, fi)
		if err != nil {
			return p9.QID{}, p9.AttrMask{}, p9.Attr{}, err
		}
		return qid, req, statToAttr(internal.InfoToStat(fi)), nil
	}

	r, err := l.openCheckedRoot(checkParent)
	if err != nil {
		return p9.QID{}, p9.AttrMask{}, p9.Attr{}, err
	}
	defer r.Close()

	fi, err := r.Lstat(relOrDot(l.rel))
	if err != nil {
		return p9.QID{}, p9.AttrMask{}, p9.Attr{}, err
	}
	qid, err := qidInRoot(r, l.rel, fi)
	if err != nil {
		return p9.QID{}, p9.AttrMask{}, p9.Attr{}, err
	}
	return qid, req, statToAttr(internal.InfoToStat(fi)), nil
}

func (l *Local) walk(names []string) ([]p9.QID, p9.File, error) {
	r, err := l.openCheckedRoot(checkDir)
	if err != nil {
		return nil, nil, err
	}
	defer r.Close()

	qids := make([]p9.QID, 0, len(names))
	rel := l.rel
	for i, name := range names {
		rel = joinRel(rel, name)
		fi, err := r.Lstat(filepath.FromSlash(rel))
		if err != nil {
			return nil, nil, err
		}
		if i < len(names)-1 {
			if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
				return nil, nil, linux.ELOOP
			}
			if fi.Mode()&os.ModeType != os.ModeDir {
				return nil, nil, linux.ENOTDIR
			}
		}
		qid, err := qidInRoot(r, rel, fi)
		if err != nil {
			return nil, nil, err
		}
		qids = append(qids, qid)
	}
	return qids, l.child(rel, nil, 0), nil
}

func (l *Local) prepareOpen(mode p9.OpenFlags) (func() (p9.QID, uint32, error), error) {
	flags, err := filterOpenFlags(mode, windowsAllowedOpenFlags)
	if err != nil {
		return nil, err
	}
	r, err := l.openCheckedRoot(checkTarget)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	rel := l.rel
	target := relOrDot(rel)
	fi, err := r.Lstat(target)
	if err != nil {
		return nil, err
	}
	wantID, err := identityInRoot(r, rel)
	if err != nil {
		return nil, err
	}
	wantType := fi.Mode().Type()

	return func() (_ p9.QID, _ uint32, err error) {
		r, err := openCheckedRootRel(l.root, rel, checkTarget)
		if err != nil {
			return p9.QID{}, 0, err
		}
		defer r.Close()

		f, err := r.OpenFile(target, flags&^os.O_TRUNC, 0)
		if err != nil {
			return p9.QID{}, 0, err
		}
		defer func() {
			if err != nil && f != nil {
				_ = f.Close()
			}
		}()

		gotID, err := fileIdentity(f)
		if err != nil {
			return p9.QID{}, 0, err
		}
		gotFI, err := f.Stat()
		if err != nil {
			return p9.QID{}, 0, err
		}
		if gotID != wantID || gotFI.Mode().Type() != wantType {
			return p9.QID{}, 0, linux.ESTALE
		}

		if flags&os.O_TRUNC != 0 {
			if gotFI.Mode().IsDir() {
				return p9.QID{}, 0, linux.EISDIR
			}
			if gotFI.Mode().IsRegular() {
				// os.Root.OpenFile omits FILE_WRITE_DATA for
				// O_APPEND without O_TRUNC (stripped above),
				// so fall back to truncateInRoot.
				if mode.Mode() != p9.ReadOnly && int(mode)&os.O_APPEND == 0 {
					err = f.Truncate(0)
				} else {
					err = truncateInRoot(r, target, f, 0)
				}
				if err != nil {
					return p9.QID{}, 0, err
				}
			}
		}

		opened := f
		f = nil
		if err = l.setOpenHandle(opened, mode); err != nil {
			return p9.QID{}, 0, err
		}
		return makeQID(gotFI.Mode(), gotID.fileIndex), 0, nil
	}, nil
}

func (l *Local) create(name string, mode p9.OpenFlags, permissions p9.FileMode) (_ p9.File, _ p9.QID, _ uint32, err error) {
	flags, err := filterOpenFlags(mode, windowsAllowedOpenFlags)
	if err != nil {
		return nil, p9.QID{}, 0, err
	}
	r, err := l.openCheckedRoot(checkDir)
	if err != nil {
		return nil, p9.QID{}, 0, err
	}
	defer r.Close()

	newRel := joinRel(l.rel, name)
	perm := os.FileMode(permissions & p9.AllPermissions)
	f, err := r.OpenFile(filepath.FromSlash(newRel), flags|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return nil, p9.QID{}, 0, err
	}
	defer func() {
		if err != nil {
			_ = f.Close()
		}
	}()

	qid, err := openFileQID(f)
	if err != nil {
		return nil, p9.QID{}, 0, err
	}
	return l.child(newRel, f, mode), qid, 0, nil
}

func (l *Local) mkdir(name string, permissions p9.FileMode) error {
	r, err := l.openCheckedRoot(checkDir)
	if err != nil {
		return err
	}
	defer r.Close()
	return r.Mkdir(filepath.FromSlash(joinRel(l.rel, name)), os.FileMode(permissions&p9.AllPermissions))
}

func (l *Local) symlink(oldname, newname string) error {
	r, err := l.openCheckedRoot(checkDir)
	if err != nil {
		return err
	}
	defer r.Close()
	return r.Symlink(oldname, filepath.FromSlash(joinRel(l.rel, newname)))
}

func (l *Local) link(target *Local, newname string) error {
	if target.rel == "" {
		return linux.EPERM
	}
	r, err := l.openCheckedRoot(checkDir)
	if err != nil {
		return err
	}
	defer r.Close()

	if err := checkPathInRoot(r, target.rel, checkParent); err != nil {
		return err
	}
	return r.Link(filepath.FromSlash(target.rel), filepath.FromSlash(joinRel(l.rel, newname)))
}

func (l *Local) renameAt(oldName string, newDir *Local, newName string) error {
	r, err := l.openCheckedRoot(checkDir)
	if err != nil {
		return err
	}
	defer r.Close()

	if newDir.rel != l.rel {
		if err := checkPathInRoot(r, newDir.rel, checkDir); err != nil {
			return err
		}
	}
	return r.Rename(
		filepath.FromSlash(joinRel(l.rel, oldName)),
		filepath.FromSlash(joinRel(newDir.rel, newName)),
	)
}

func (l *Local) readlink() (string, error) {
	if l.rel == "" {
		return "", linux.EINVAL
	}
	r, err := l.openCheckedRoot(checkParent)
	if err != nil {
		return "", err
	}
	defer r.Close()
	return r.Readlink(filepath.FromSlash(l.rel))
}

func truncateInRoot(root *os.Root, target string, openFile *os.File, size int64) (err error) {
	f, err := root.OpenFile(target, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
	}()
	if openFile != nil {
		if err = verifySameFile(f, openFile); err != nil {
			return err
		}
	}
	return f.Truncate(size)
}

func (l *Local) setAttr(valid p9.SetAttrMask, attr p9.SetAttr) error {
	h := l.open.Load()
	var openFile *os.File
	if h != nil {
		openFile = h.file
	}
	var r *os.Root
	defer func() {
		if r != nil {
			_ = r.Close()
		}
	}()
	ensureRoot := func() (*os.Root, error) {
		if r == nil {
			var err error
			r, err = l.openCheckedRoot(checkTarget)
			if err != nil {
				return nil, err
			}
		}
		if openFile != nil {
			if err := verifyRootTargetMatchesFile(r, l.rel, openFile); err != nil {
				return nil, err
			}
		}
		return r, nil
	}

	target := relOrDot(l.rel)

	// Perform Size, ATime/MTime, and UID/GID before Permissions so that a
	// single SetAttr call setting read-only permissions does not fail its
	// own truncate or chtimes.
	if valid.Size {
		// Go's os.Root.OpenFile omits FILE_WRITE_DATA for O_APPEND
		// without O_TRUNC, so O_APPEND fids take the verified reopen
		// path to avoid ERROR_ACCESS_DENIED on Truncate.
		if h != nil && h.mode.Mode() != p9.ReadOnly && int(h.mode)&os.O_APPEND == 0 {
			if err := h.file.Truncate(int64(attr.Size)); err != nil {
				return err
			}
		} else {
			root, err := ensureRoot()
			if err != nil {
				return err
			}
			if err := truncateInRoot(root, target, openFile, int64(attr.Size)); err != nil {
				return err
			}
		}
	}
	if valid.ATime || valid.MTime {
		root, err := ensureRoot()
		if err != nil {
			return err
		}
		atime, mtime := resolveTimes(valid, attr)
		if err := root.Chtimes(target, atime, mtime); err != nil {
			return err
		}
	}
	if valid.UID || valid.GID {
		root, err := ensureRoot()
		if err != nil {
			return err
		}
		uid, gid := -1, -1
		if valid.UID {
			uid = int(attr.UID)
		}
		if valid.GID {
			gid = int(attr.GID)
		}
		if err := root.Lchown(target, uid, gid); err != nil {
			return err
		}
	}
	if valid.Permissions {
		root, err := ensureRoot()
		if err != nil {
			return err
		}
		if err := root.Chmod(target, attr.Permissions.Permissions().OSMode()); err != nil {
			return err
		}
	}
	return nil
}

func (l *Local) remove() error {
	if l.rel == "" {
		return linux.EINVAL
	}
	r, err := l.openCheckedRoot(checkParent)
	if err != nil {
		return err
	}
	defer r.Close()
	return r.Remove(filepath.FromSlash(l.rel))
}

func (l *Local) unlinkAt(name string, _ uint32) error {
	r, err := l.openCheckedRoot(checkDir)
	if err != nil {
		return err
	}
	defer r.Close()
	return r.Remove(filepath.FromSlash(joinRel(l.rel, name)))
}

func (l *Local) readDir(offset uint64, count uint32) (p9.Dirents, error) {
	r, err := l.openCheckedRoot(checkDir)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	f, err := r.Open(relOrDot(l.rel))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if h := l.open.Load(); h != nil {
		if err := verifySameFile(f, h.file); err != nil {
			return nil, err
		}
	}

	entries, err := f.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int {
		return cmp.Compare(a.Name(), b.Name())
	})
	if offset >= uint64(len(entries)) {
		return nil, nil
	}

	var dirents p9.Dirents
	err = withFileHandle(f, func(dirHandle windows.Handle) error {
		for i, de := range entries[offset:] {
			if uint64(len(dirents)) >= uint64(count) {
				break
			}
			id, err := fileIdentityAt(dirHandle, de.Name())
			if err != nil {
				continue
			}
			qid := makeQID(de.Type(), id.fileIndex)
			dirents = append(dirents, p9.Dirent{
				QID:    qid,
				Type:   qid.Type,
				Name:   de.Name(),
				Offset: offset + uint64(i) + 1,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return dirents, nil
}

// lock implements p9.File.Lock.
// As in FreeBSD NFS locking, we just say "sure, we did it" without actually
// doing anything; this lock design makes even less sense on Windows than
// it does on Linux (pid? really? what were they thinking?)
func (l *Local) lock(pid int, locktype p9.LockType, flags p9.LockFlags, start, length uint64, client string) (p9.LockStatus, error) {
	return p9.LockStatusOK, linux.ENOSYS
}
