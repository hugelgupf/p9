package localfs

import (
	"os"
	"path/filepath"

	"github.com/hugelgupf/p9/internal"
	"github.com/hugelgupf/p9/linux"
	"github.com/hugelgupf/p9/p9"
	"golang.org/x/sys/windows"
)

func umask(_ int) int {
	return 0
}

// Temporary mechanical bridge for Commit 2; Commit 3 replaces this with full
// os.Root and NtCreateFile confinement on Windows.
type rootDir struct {
	id   string
	path string
}

func openRootDir(path string) (*rootDir, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	return &rootDir{id: abs, path: abs}, nil
}

func (r *rootDir) acquireFID() {}
func (r *rootDir) releaseFID() {}

func (l *Local) hostPath() (string, error) {
	if l.root == nil {
		return "", linux.EBADF
	}
	if err := validateRel(l.rel); err != nil {
		return "", err
	}
	return filepath.Join(l.root.path, filepath.FromSlash(l.rel)), nil
}

func localToQid(path string, info os.FileInfo) (uint64, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}

	var (
		access     uint32 // none; we only need metadata
		sharemode  uint32
		createmode uint32 = windows.OPEN_EXISTING
		attribute  uint32 = windows.FILE_ATTRIBUTE_NORMAL
	)
	if info.IsDir() {
		attribute = windows.FILE_FLAG_BACKUP_SEMANTICS
	}
	fd, err := windows.CreateFile(pathPtr, access, sharemode, nil, createmode, attribute, 0)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(fd)

	fi := &windows.ByHandleFileInformation{}
	if err = windows.GetFileInformationByHandle(fd, fi); err != nil {
		return 0, err
	}

	x := uint64(fi.FileIndexHigh)<<32 | uint64(fi.FileIndexLow)
	return x, nil
}

func qidForPath(p string, fi os.FileInfo) (p9.QID, error) {
	ninePath, err := localToQid(p, fi)
	if err != nil {
		return p9.QID{}, err
	}
	return p9.QID{
		Type: p9.ModeFromOS(fi.Mode()).QIDType(),
		Path: ninePath,
	}, nil
}

func (l *Local) info() (p9.QID, os.FileInfo, error) {
	p, err := l.hostPath()
	if err != nil {
		return p9.QID{}, nil, err
	}
	var fi os.FileInfo
	if l.file != nil {
		fi, err = l.file.Stat()
	} else {
		fi, err = os.Lstat(p)
	}
	if err != nil {
		return p9.QID{}, nil, err
	}
	qid, err := qidForPath(p, fi)
	if err != nil {
		return p9.QID{}, nil, err
	}
	return qid, fi, nil
}

func (l *Local) getAttr(req p9.AttrMask) (p9.QID, p9.AttrMask, p9.Attr, error) {
	qid, fi, err := l.info()
	if err != nil {
		return qid, p9.AttrMask{}, p9.Attr{}, err
	}
	return qid, req, statToAttr(internal.InfoToStat(fi)), nil
}

func (l *Local) walk(names []string) ([]p9.QID, p9.File, error) {
	base, err := l.hostPath()
	if err != nil {
		return nil, nil, err
	}
	qids := make([]p9.QID, 0, len(names))
	rel := l.rel
	p := base
	for _, name := range names {
		rel = joinRel(rel, name)
		p = filepath.Join(p, name)
		fi, err := os.Lstat(p)
		if err != nil {
			return nil, nil, err
		}
		qid, err := qidForPath(p, fi)
		if err != nil {
			return nil, nil, err
		}
		qids = append(qids, qid)
	}
	return qids, l.child(rel, nil, 0), nil
}

func (l *Local) open(mode p9.OpenFlags) (p9.QID, uint32, error) {
	p, err := l.hostPath()
	if err != nil {
		return p9.QID{}, 0, err
	}
	qid, fi, err := l.info()
	if err != nil {
		return qid, 0, err
	}
	if fi.IsDir() && mode == p9.WriteOnly {
		return qid, 0, linux.EISDIR
	}
	f, err := os.OpenFile(p, int(mode), 0)
	if err != nil {
		return qid, 0, err
	}
	l.file = f
	l.mode = mode
	return qid, 0, nil
}

func (l *Local) create(name string, mode p9.OpenFlags, permissions p9.FileMode) (p9.File, p9.QID, uint32, error) {
	dir, err := l.hostPath()
	if err != nil {
		return nil, p9.QID{}, 0, err
	}
	newRel := joinRel(l.rel, name)
	newPath := filepath.Join(dir, name)
	f, err := os.OpenFile(newPath, int(mode)|os.O_CREATE|os.O_EXCL, os.FileMode(permissions))
	if err != nil {
		return nil, p9.QID{}, 0, err
	}
	l2 := l.child(newRel, f, mode)
	qid, _, err := l2.info()
	if err != nil {
		_ = l2.Close()
		return nil, p9.QID{}, 0, err
	}
	return l2, qid, 0, nil
}

func (l *Local) mkdir(name string, permissions p9.FileMode) error {
	dir, err := l.hostPath()
	if err != nil {
		return err
	}
	return os.Mkdir(filepath.Join(dir, name), os.FileMode(permissions))
}

func (l *Local) symlink(oldname, newname string) error {
	dir, err := l.hostPath()
	if err != nil {
		return err
	}
	return os.Symlink(oldname, filepath.Join(dir, newname))
}

func (l *Local) link(target *Local, newname string) error {
	dir, err := l.hostPath()
	if err != nil {
		return err
	}
	targetPath, err := target.hostPath()
	if err != nil {
		return err
	}
	return os.Link(targetPath, filepath.Join(dir, newname))
}

func (l *Local) renameAt(oldName string, newDir *Local, newName string) error {
	oldDir, err := l.hostPath()
	if err != nil {
		return err
	}
	dstDir, err := newDir.hostPath()
	if err != nil {
		return err
	}
	return os.Rename(filepath.Join(oldDir, oldName), filepath.Join(dstDir, newName))
}

func (l *Local) readlink() (string, error) {
	p, err := l.hostPath()
	if err != nil {
		return "", err
	}
	return os.Readlink(p)
}

func (l *Local) setAttr(valid p9.SetAttrMask, attr p9.SetAttr) error {
	p, err := l.hostPath()
	if err != nil {
		return err
	}
	if valid.Size {
		if err := os.Truncate(p, int64(attr.Size)); err != nil {
			return err
		}
	}
	if valid.ATime || valid.MTime {
		atime, mtime := resolveTimes(valid, attr)
		if err := os.Chtimes(p, atime, mtime); err != nil {
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
		if err := os.Chown(p, uid, gid); err != nil {
			return err
		}
	}
	if valid.Permissions {
		if err := os.Chmod(p, attr.Permissions.Permissions().OSMode()); err != nil {
			return err
		}
	}
	return nil
}

func (l *Local) remove() error {
	p, err := l.hostPath()
	if err != nil {
		return err
	}
	return os.Remove(p)
}

func (l *Local) unlinkAt(name string, _ uint32) error {
	dir, err := l.hostPath()
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(dir, name))
}

func (l *Local) readDir(offset uint64, count uint32) (p9.Dirents, error) {
	dir, err := l.hostPath()
	if err != nil {
		return nil, err
	}
	fi, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	if offset >= uint64(len(fi)) {
		return nil, nil
	}
	var dirents p9.Dirents
	for i := int(offset); i < len(fi) && uint64(len(dirents)) < uint64(count); i++ {
		entryPath := filepath.Join(dir, fi[i].Name())
		info, err := os.Lstat(entryPath)
		if err != nil {
			continue
		}
		qid, err := qidForPath(entryPath, info)
		if err != nil {
			continue
		}
		dirents = append(dirents, p9.Dirent{
			QID:    qid,
			Type:   qid.Type,
			Name:   fi[i].Name(),
			Offset: uint64(i + 1),
		})
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
