//go:build !windows && !linux

package localfs

import (
	"os"

	"github.com/hugelgupf/p9/p9"
	"golang.org/x/sys/unix"
)

const openDirFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOCTTY | unix.O_CLOEXEC

func openDirRel(rootFD int, rel string) (int, error) {
	return openDirStepByStep(rootFD, rel)
}

func fchmodAtNoFollow(dirFD int, name string, mode uint32) error {
	return ignoringEINTR(func() error {
		return unix.Fchmodat(dirFD, name, mode, unix.AT_SYMLINK_NOFOLLOW)
	})
}

func truncateAtNoFollow(dirFD int, name string, size int64, want *unix.Stat_t) error {
	var st unix.Stat_t
	if err := fstatAt(dirFD, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if want != nil && !sameInode(&st, want) {
		return unix.ESTALE
	}
	if err := checkRegularForTruncate(&st); err != nil {
		return err
	}

	fd, err := openVerifiedFD(dirFD, name, unix.O_WRONLY, &st)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	return ignoringEINTR(func() error {
		return unix.Ftruncate(fd, size)
	})
}

func ftruncateReadOnly(_ *os.File, _ int64) error {
	return unix.ENOSYS
}

func futimesNano(_ *os.File, _ *[2]unix.Timespec) error {
	return unix.ENOSYS
}

func (l *Local) prepareOpen(mode p9.OpenFlags) (_ func() (p9.QID, uint32, error), err error) {
	flags, err := filterOpenFlags(mode, unixAllowedOpenFlags)
	if err != nil {
		return nil, err
	}
	dirFD, release, name, err := l.openTarget()
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			release()
		}
	}()
	displayName := l.displayName()

	var st unix.Stat_t
	if err = fstatAt(dirFD, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, err
	}
	if st.Mode&unix.S_IFMT == unix.S_IFLNK {
		return nil, unix.ELOOP
	}

	return func() (p9.QID, uint32, error) {
		defer release()
		return l.openVerifiedTarget(dirFD, name, flags, mode, displayName, &st)
	}, nil
}
