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
func (l *Local) lock(pid, locktype, flags int, start, length uint64, client string) error {
	var err error
	switch p9.LockType(locktype) {
	case p9.ReadLock, p9.WriteLock:
		if err := unix.Flock(int(l.file.Fd()), unix.LOCK_EX); err != nil {
			err = p9.ELockError
		}
	case p9.Unlock:
		if err := unix.Flock(int(l.file.Fd()), unix.LOCK_EX); err != nil {
			err = p9.ELockError
		}
	default:
		// 9P2000.L not only does not return an Rerror per standard,
		// it does not have a way to say "not implemented" for a Lock type.
		// They had 255 possible values, and use 3, ... ay yi yi.
		// 9P2000.L doesn't really fit the spirit of 9P.
		err = p9.ELockError
	}
	return err
}
