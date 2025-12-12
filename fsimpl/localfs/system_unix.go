//go:build !windows
// +build !windows

package localfs

import (
	"os"
	"path/filepath"
	"syscall"

	"github.com/hugelgupf/p9/p9"
	"golang.org/x/sys/unix"
)

func umask(mask int) int {
	return syscall.Umask(mask)
}

func localToQid(_ string, fi os.FileInfo, _ *os.File) (uint64, error) {
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

func statFSForPath(p string) (p9.FSStat, error) {
	// Use statvfs; it’s closer to FUSE’s Statfs_t.
	var st unix.Statfs_t
	if err := unix.Statfs(filepath.Dir(p), &st); err != nil {
		return p9.FSStat{}, err
	}
	// Map fields. Note: linux/unix Statfs_t semantics:
	// f_bsize: optimal transfer block size
	// f_frsize: fundamental filesystem block size (not always present; on linux Statfs_t doesn’t have frsize)
	// We’ll use f_bsize for both if frsize not available.
	blockSize := uint32(st.Bsize)
	return p9.FSStat{
		Type:            uint32(st.Type),
		BlockSize:       blockSize,
		Blocks:          uint64(st.Blocks),
		BlocksFree:      uint64(st.Bfree),
		BlocksAvailable: uint64(st.Bavail),
		Files:           uint64(st.Files),
		FilesFree:       uint64(st.Ffree),
		// FIXME: Pretty sure macos is different about this value.
		// FSID:            uint64(st.Fsid.X__val[0]), // best-effort; platform-specific
		// FIXME: fetch it.
		// NameLength:      255,                       // use a sane default or query if available
	}, nil
}
