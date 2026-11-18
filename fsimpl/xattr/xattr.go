//go:build unix && !openbsd && !solaris

package xattr

import (
	"errors"
	"io/fs"
	"strings"

	"golang.org/x/sys/unix"
)

// maxXattrRetries bounds retries when an attribute or attribute list grows
// concurrently between the size probe and the read. 18 attempts allow doubling
// from 1 byte past Linux's 64 KiB XATTR_SIZE_MAX / XATTR_LIST_MAX even if size
// probes keep reporting 0.
const maxXattrRetries = 18

func readXattr(p, op string, fn func(dest []byte) (int, error)) ([]byte, error) {
	minAlloc := 1
	for range maxXattrRetries {
		sz, err := fn(nil)
		if err != nil {
			return nil, &fs.PathError{Op: op + "-get-size", Path: p, Err: err}
		}
		if sz < 0 {
			return nil, &fs.PathError{Op: op + "-get-size", Path: p, Err: unix.EIO}
		}

		// Always pass len(b) >= 1 on the read call: passing an empty
		// slice to lgetxattr/llistxattr performs another size probe
		// instead of a bounded read and would not fail with ERANGE if
		// the attribute grew concurrently.
		b := make([]byte, max(sz, minAlloc))
		n, err := fn(b)
		if errors.Is(err, unix.ERANGE) {
			minAlloc = len(b) * 2
			continue
		}
		if err != nil {
			return nil, &fs.PathError{Op: op, Path: p, Err: err}
		}
		if n < 0 || n > len(b) {
			minAlloc = max(len(b)*2, n)
			continue
		}
		return b[:n], nil
	}
	return nil, &fs.PathError{Op: op, Path: p, Err: unix.ERANGE}
}

func listWith(p string, fn func(dest []byte) (int, error)) ([]string, error) {
	raw, err := readXattr(p, "listxattr", fn)
	if err != nil || len(raw) == 0 {
		return nil, err
	}

	var names []string
	for _, name := range strings.Split(string(raw), "\x00") {
		if name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

// List lists extended attribute names on p without following symlinks, matching
// 9P2000.L server semantics where symlink resolution is performed client-side.
func List(p string) ([]string, error) {
	return listWith(p, func(dest []byte) (int, error) {
		return unix.Llistxattr(p, dest)
	})
}

// Get reads the extended attribute attr on p without following symlinks,
// matching 9P2000.L server semantics where symlink resolution is client-side.
func Get(p string, attr string) ([]byte, error) {
	return readXattr(p, "getxattr", func(dest []byte) (int, error) {
		return unix.Lgetxattr(p, attr, dest)
	})
}
