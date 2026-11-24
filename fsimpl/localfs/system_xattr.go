//go:build unix && !solaris && !openbsd

package localfs

import (
	"path/filepath"

	"github.com/hugelgupf/p9/fsimpl/xattr"
	"github.com/hugelgupf/p9/linux"
	"github.com/hugelgupf/p9/p9"
	"golang.org/x/sys/unix"
)

func (l *Local) xattrPath() (string, error) {
	if l.root == nil {
		return "", linux.EBADF
	}
	if err := validateRel(l.rel); err != nil {
		return "", err
	}
	return filepath.Join(l.root.path, filepath.FromSlash(l.rel)), nil
}

func (l *Local) SetXattr(attr string, data []byte, flags p9.XattrFlags) error {
	p, err := l.xattrPath()
	if err != nil {
		return err
	}
	return unix.Setxattr(p, attr, data, int(flags))
}

func (l *Local) ListXattrs() ([]string, error) {
	p, err := l.xattrPath()
	if err != nil {
		return nil, err
	}
	return xattr.List(p)
}

func (l *Local) GetXattr(attr string) ([]byte, error) {
	p, err := l.xattrPath()
	if err != nil {
		return nil, err
	}
	return xattr.Get(p, attr)
}

func (l *Local) RemoveXattr(attr string) error {
	p, err := l.xattrPath()
	if err != nil {
		return err
	}
	return unix.Removexattr(p, attr)
}
