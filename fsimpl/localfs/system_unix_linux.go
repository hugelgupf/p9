//go:build linux

package localfs

import (
	"os"
	"strconv"
	"sync/atomic"

	"github.com/hugelgupf/p9/p9"
	"golang.org/x/sys/unix"
)

// openat2 with O_PATH permits only O_DIRECTORY, O_NOFOLLOW, and O_CLOEXEC
// (O_PATH never opens a controlling terminal anyway).
const openDirFlags = unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC

var openat2Unsupported atomic.Bool

func openDirRel(rootFD int, rel string) (int, error) {
	if !openat2Unsupported.Load() {
		how := &unix.OpenHow{
			Flags:   openDirFlags,
			Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_BENEATH,
		}
		fd, err := ignoringEINTRInt(func() (int, error) {
			return unix.Openat2(rootFD, rel, how)
		})
		if err == nil {
			return fd, nil
		}
		if err == unix.ENOSYS {
			openat2Unsupported.Store(true)
		} else if err != unix.EPERM {
			return -1, err
		}
		// Older seccomp policies block openat2 with EPERM; fall back
		// to step-by-step traversal without caching EPERM globally.
	}
	return openDirStepByStep(rootFD, rel)
}

func procFDPath(fd int) string {
	return "/proc/self/fd/" + strconv.Itoa(fd)
}

func fchmodAtNoFollow(dirFD int, name string, mode uint32) error {
	err := ignoringEINTR(func() error {
		return unix.Fchmodat(dirFD, name, mode, unix.AT_SYMLINK_NOFOLLOW)
	})
	if err != unix.EOPNOTSUPP && err != unix.EPERM {
		return err
	}
	return fchmodAtNoFollowFallback(dirFD, name, mode)
}

func fchmodAtNoFollowFallback(dirFD int, name string, mode uint32) error {
	// Target is a symlink, this kernel lacks fchmodat2 (< 6.6), or seccomp
	// blocked fchmodat2 with EPERM. Pin via O_PATH|O_NOFOLLOW to reject
	// symlinks cleanly, then chmod through /proc/self/fd.
	fd, err := openAt(dirFD, name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)

	var st unix.Stat_t
	if err := fstat(fd, &st); err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT == unix.S_IFLNK {
		return unix.EOPNOTSUPP
	}

	err = ignoringEINTR(func() error {
		return unix.Chmod(procFDPath(fd), mode)
	})
	if err != unix.ENOENT {
		return err
	}

	// /proc is not mounted; fall back to O_RDONLY|O_NOFOLLOW + fchmod.
	// Only allow regular files and directories so we never open FIFOs,
	// sockets, or device nodes.
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFREG, unix.S_IFDIR:
	default:
		return unix.EOPNOTSUPP
	}
	fd2, err := openVerifiedFD(dirFD, name, unix.O_RDONLY, &st)
	if err != nil {
		return err
	}
	defer unix.Close(fd2)
	return ignoringEINTR(func() error {
		return unix.Fchmod(fd2, mode)
	})
}

func truncateAtNoFollow(dirFD int, name string, size int64, want *unix.Stat_t) error {
	// Pin via O_PATH|O_NOFOLLOW and verify S_IFREG before truncating so we
	// never open FIFOs or device nodes with O_WRONLY.
	fd, err := openAt(dirFD, name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)

	var st unix.Stat_t
	if err := fstat(fd, &st); err != nil {
		return err
	}
	if want != nil && !sameInode(&st, want) {
		return unix.ESTALE
	}
	if err := checkRegularForTruncate(&st); err != nil {
		return err
	}

	err = ignoringEINTR(func() error {
		return unix.Truncate(procFDPath(fd), size)
	})
	if err != unix.ENOENT {
		return err
	}

	// /proc is not mounted; fall back to O_WRONLY|O_NOFOLLOW + ftruncate.
	fd2, err := openVerifiedFD(dirFD, name, unix.O_WRONLY, &st)
	if err != nil {
		return err
	}
	defer unix.Close(fd2)
	return ignoringEINTR(func() error {
		return unix.Ftruncate(fd2, size)
	})
}

func ftruncateReadOnly(f *os.File, size int64) error {
	return withFileFD(f, func(fd int) error {
		var st unix.Stat_t
		if err := fstat(fd, &st); err != nil {
			return err
		}
		if err := checkRegularForTruncate(&st); err != nil {
			return err
		}
		return ignoringEINTR(func() error {
			return unix.Truncate(procFDPath(fd), size)
		})
	})
}

func futimesNano(f *os.File, ts *[2]unix.Timespec) error {
	return withFileFD(f, func(fd int) error {
		return ignoringEINTR(func() error {
			return unix.UtimesNanoAt(unix.AT_FDCWD, procFDPath(fd), ts[:], 0)
		})
	})
}

func (l *Local) prepareOpen(mode p9.OpenFlags) (func() (p9.QID, uint32, error), error) {
	return l.prepareOpenWithProc(mode, procFDPath)
}

func (l *Local) prepareOpenWithProc(mode p9.OpenFlags, procPath func(int) string) (func() (p9.QID, uint32, error), error) {
	flags, err := filterOpenFlags(mode, unixAllowedOpenFlags)
	if err != nil {
		return nil, err
	}
	dirFD, release, name, err := l.openTarget()
	if err != nil {
		return nil, err
	}
	releaseDir := true
	defer func() {
		if releaseDir {
			release()
		}
	}()
	displayName := l.displayName()

	// Pin the target inode with O_PATH|O_NOFOLLOW under the server's read
	// locks so finish can open /proc/self/fd/<pinFD> after dropping them.
	pinFD, err := openAt(dirFD, name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	closePin := true
	defer func() {
		if closePin {
			_ = unix.Close(pinFD)
		}
	}()

	var st unix.Stat_t
	if err := fstat(pinFD, &st); err != nil {
		return nil, err
	}
	if st.Mode&unix.S_IFMT == unix.S_IFLNK {
		return nil, unix.ELOOP
	}

	// Probe /proc/self/fd via stat (never blocks, even on FIFOs). If it is
	// usable and resolves to the same inode, release dirFD now so finish
	// needs only pinFD. Do not pass O_NOFOLLOW when opening
	// /proc/self/fd/<pinFD>, as procfs presents it as a magic symlink
	// (symlinks were already rejected above).
	procTarget := procPath(pinFD)
	var pst unix.Stat_t
	procErr := ignoringEINTR(func() error {
		return unix.Stat(procTarget, &pst)
	})
	if procErr == nil && sameInode(&pst, &st) {
		closePin = false
		return func() (p9.QID, uint32, error) {
			defer unix.Close(pinFD)
			fd, err := openAt(unix.AT_FDCWD, procTarget, flags|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
			if err != nil {
				return p9.QID{}, 0, err
			}
			f := os.NewFile(uintptr(fd), displayName)
			if err := l.setOpenHandle(f, mode); err != nil {
				return p9.QID{}, 0, err
			}
			return statToQID(&st), 0, nil
		}, nil
	}

	// /proc is absent or inaccessible; keep (dirFD, release, name, st) to
	// open with O_NOFOLLOW and inode verification in finish.
	releaseDir = false
	return func() (p9.QID, uint32, error) {
		defer release()
		return l.openVerifiedTarget(dirFD, name, flags, mode, displayName, &st)
	}, nil
}
