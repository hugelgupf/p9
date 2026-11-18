//go:build unix && !solaris && !openbsd

package localfs

import (
	"github.com/hugelgupf/p9/fsimpl/xattr"
	"github.com/hugelgupf/p9/p9"
	"golang.org/x/sys/unix"
)

// In 9P2000.L, the server never follows symlinks automatically during walk or
// xattr operations; symlink resolution is strictly a client-side responsibility
// (via Treadlink). Use the no-follow L*xattr variants here so xattr calls
// operate on the walked node itself without escaping across host symlinks or
// failing with ENOENT on dangling symlinks.

func (l *Local) SetXattr(attr string, data []byte, flags p9.XattrFlags) error {
	return unix.Lsetxattr(l.path, attr, data, int(flags))
}

func (l *Local) ListXattrs() ([]string, error) {
	return xattr.List(l.path)
}

func (l *Local) GetXattr(attr string) ([]byte, error) {
	return xattr.Get(l.path, attr)
}

func (l *Local) RemoveXattr(attr string) error {
	return unix.Lremovexattr(l.path, attr)
}
