//go:build linux || darwin || freebsd || netbsd

package localfs

import (
	"github.com/hugelgupf/p9/p9"
	"golang.org/x/sys/unix"
)

func (l *Local) withXattr(fdFn func(fd int) error, atFn func(parentFD int, base string) error) error {
	if h := l.open.Load(); h != nil {
		return withFileFD(h.file, fdFn)
	}
	if l.rel == "" {
		dirFD, release, err := l.openDir()
		if err != nil {
			return err
		}
		defer release()
		return fdFn(dirFD)
	}
	parentFD, release, base, err := l.openParent()
	if err != nil {
		return err
	}
	defer release()
	return atFn(parentFD, base)
}

func readXattrBytes(fn func(dest []byte) (int, error)) ([]byte, error) {
	for {
		sz, err := ignoringEINTRInt(func() (int, error) {
			return fn(nil)
		})
		if err != nil {
			return nil, err
		}
		if sz == 0 {
			return []byte{}, nil
		}
		// Allocate an extra byte so platforms that silently truncate
		// (FreeBSD/NetBSD extattr) trigger a re-probe if the value
		// grew between calls.
		buf := make([]byte, sz+1)
		got, err := ignoringEINTRInt(func() (int, error) {
			return fn(buf)
		})
		if err == unix.ERANGE {
			continue
		}
		if err != nil {
			return nil, err
		}
		if got == len(buf) {
			continue
		}
		return buf[:got], nil
	}
}

func setXattrFD(fd int, attr string, data []byte, flags p9.XattrFlags) error {
	return ignoringEINTR(func() error {
		return unix.Fsetxattr(fd, attr, data, int(flags))
	})
}

func getXattrFD(fd int, attr string) ([]byte, error) {
	return readXattrBytes(func(dest []byte) (int, error) {
		return unix.Fgetxattr(fd, attr, dest)
	})
}

func removeXattrFD(fd int, attr string) error {
	return ignoringEINTR(func() error {
		return unix.Fremovexattr(fd, attr)
	})
}

// SetXattr implements p9.File.SetXattr.
//
// Note: Darwin, FreeBSD, and NetBSD unix.Fsetxattr ignore flags.
func (l *Local) SetXattr(attr string, data []byte, flags p9.XattrFlags) error {
	return l.withXattr(
		func(fd int) error { return setXattrFD(fd, attr, data, flags) },
		func(parentFD int, base string) error { return setXattrAt(parentFD, base, attr, data, flags) },
	)
}

// ListXattrs implements p9.File.ListXattrs.
func (l *Local) ListXattrs() ([]string, error) {
	var list []string
	err := l.withXattr(
		func(fd int) error {
			var e error
			list, e = listXattrsFD(fd)
			return e
		},
		func(parentFD int, base string) error {
			var e error
			list, e = listXattrsAt(parentFD, base)
			return e
		},
	)
	return list, err
}

// GetXattr implements p9.File.GetXattr.
func (l *Local) GetXattr(attr string) ([]byte, error) {
	var out []byte
	err := l.withXattr(
		func(fd int) error {
			var e error
			out, e = getXattrFD(fd, attr)
			return e
		},
		func(parentFD int, base string) error {
			var e error
			out, e = getXattrAt(parentFD, base, attr)
			return e
		},
	)
	return out, err
}

// RemoveXattr implements p9.File.RemoveXattr.
func (l *Local) RemoveXattr(attr string) error {
	return l.withXattr(
		func(fd int) error { return removeXattrFD(fd, attr) },
		func(parentFD int, base string) error { return removeXattrAt(parentFD, base, attr) },
	)
}
