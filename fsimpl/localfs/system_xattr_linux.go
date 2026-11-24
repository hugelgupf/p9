//go:build linux

package localfs

import (
	"github.com/hugelgupf/p9/p9"
	"golang.org/x/sys/unix"
)

// withPinnedAt pins (parentFD, base) with O_PATH|O_NOFOLLOW and invokes pathFn
// on /proc/self/fd/<fd> so xattr ops act on the pinned inode (including
// symlinks and mode-0000 files) without following symlinks or blocking on a
// FIFO. If /proc is absent, it verifies the inode type and falls back to
// O_RDONLY|O_NOFOLLOW + fdFn.
func withPinnedAt(parentFD int, base string, pathFn func(path string) error, fdFn func(fd int) error) error {
	fd, err := openAt(parentFD, base, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)

	err = pathFn(procFDPath(fd))
	if err != unix.ENOENT {
		return err
	}

	// /proc is not mounted; inspect the pinned inode before opening it so
	// we never open symlinks, FIFOs, sockets, or device nodes.
	var st unix.Stat_t
	if err := fstat(fd, &st); err != nil {
		return err
	}
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFREG, unix.S_IFDIR:
	case unix.S_IFLNK:
		return unix.ELOOP
	default:
		return unix.EOPNOTSUPP
	}

	fd2, err := openVerifiedFD(parentFD, base, unix.O_RDONLY, &st)
	if err != nil {
		return err
	}
	defer unix.Close(fd2)
	return fdFn(fd2)
}

func setXattrAt(parentFD int, base, attr string, data []byte, flags p9.XattrFlags) error {
	return withPinnedAt(parentFD, base,
		func(p string) error {
			return ignoringEINTR(func() error { return unix.Setxattr(p, attr, data, int(flags)) })
		},
		func(fd int) error { return setXattrFD(fd, attr, data, flags) },
	)
}

func listXattrsAt(parentFD int, base string) ([]string, error) {
	var list []string
	err := withPinnedAt(parentFD, base,
		func(p string) error {
			raw, e := readXattrBytes(func(dest []byte) (int, error) {
				return unix.Listxattr(p, dest)
			})
			if e != nil {
				return e
			}
			list = parseNulXattrList(raw)
			return nil
		},
		func(fd int) error {
			var e error
			list, e = listXattrsFD(fd)
			return e
		},
	)
	return list, err
}

func getXattrAt(parentFD int, base, attr string) ([]byte, error) {
	var out []byte
	err := withPinnedAt(parentFD, base,
		func(p string) error {
			var e error
			out, e = readXattrBytes(func(dest []byte) (int, error) {
				return unix.Getxattr(p, attr, dest)
			})
			return e
		},
		func(fd int) error {
			var e error
			out, e = getXattrFD(fd, attr)
			return e
		},
	)
	return out, err
}

func removeXattrAt(parentFD int, base, attr string) error {
	return withPinnedAt(parentFD, base,
		func(p string) error {
			return ignoringEINTR(func() error { return unix.Removexattr(p, attr) })
		},
		func(fd int) error { return removeXattrFD(fd, attr) },
	)
}
