//go:build darwin || freebsd || netbsd

package localfs

import (
	"github.com/hugelgupf/p9/p9"
	"golang.org/x/sys/unix"
)

func withPinnedAt(parentFD int, base string, fdFn func(fd int) error) error {
	var st unix.Stat_t
	if err := fstatAt(parentFD, base, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFREG, unix.S_IFDIR:
	case unix.S_IFLNK:
		return unix.ELOOP
	default:
		// Never open FIFOs, sockets, or devices for xattr probes.
		return unix.EOPNOTSUPP
	}

	fd, err := openVerifiedFD(parentFD, base, unix.O_RDONLY, &st)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	return fdFn(fd)
}

func setXattrAt(parentFD int, base, attr string, data []byte, flags p9.XattrFlags) error {
	return withPinnedAt(parentFD, base, func(fd int) error {
		return setXattrFD(fd, attr, data, flags)
	})
}

func listXattrsAt(parentFD int, base string) ([]string, error) {
	var list []string
	err := withPinnedAt(parentFD, base, func(fd int) error {
		var e error
		list, e = listXattrsFD(fd)
		return e
	})
	return list, err
}

func getXattrAt(parentFD int, base, attr string) ([]byte, error) {
	var out []byte
	err := withPinnedAt(parentFD, base, func(fd int) error {
		var e error
		out, e = getXattrFD(fd, attr)
		return e
	})
	return out, err
}

func removeXattrAt(parentFD int, base, attr string) error {
	return withPinnedAt(parentFD, base, func(fd int) error {
		return removeXattrFD(fd, attr)
	})
}
