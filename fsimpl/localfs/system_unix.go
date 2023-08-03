//go:build !windows
// +build !windows

package localfs

import (
	"os"
	"syscall"

	"github.com/hugelgupf/p9/p9"
	"golang.org/x/sys/unix"
)

func umask(mask int) int {
	return syscall.Umask(mask)
}

func localToQid(_ string, fi os.FileInfo) (uint64, error) {
	return uint64(fi.Sys().(*syscall.Stat_t).Ino), nil
}

// lock implements p9.File.Lock.
func (l *Local) lock(pid int, locktype p9.LockType, flags p9.LockFlags, start, length uint64, client string) (p9.LockStatus, error) {
	switch locktype {
	case p9.ReadLock, p9.WriteLock:
		if err := unix.Flock(int(l.file.Fd()), unix.LOCK_EX); err != nil {
			return p9.LockStatusError, nil
		}

	case p9.Unlock:
		if err := unix.Flock(int(l.file.Fd()), unix.LOCK_EX); err != nil {
			return p9.LockStatusError, nil
		}

	default:
		return p9.LockStatusOK, unix.ENOSYS
	}

	return p9.LockStatusOK, nil
}

func (l *Local) SetXattr(attr string, data []byte, flags int) error {
	return unix.Setxattr(l.path, attr, data, flags)
}

func (l *Local) ListXattrs(buf []byte) (int, error) {
	return unix.Listxattr(l.path, buf)
}

func (l *Local) GetXattr(attr string, buf []byte) (int, error) {
	return unix.Getxattr(l.path, attr, buf)
}
