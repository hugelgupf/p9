//go:build linux || darwin

package localfs

import (
	"strings"

	"golang.org/x/sys/unix"
)

func parseNulXattrList(raw []byte) []string {
	trimmed := strings.Trim(string(raw), "\x00")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\x00")
}

func listXattrsFD(fd int) ([]string, error) {
	raw, err := readXattrBytes(func(dest []byte) (int, error) {
		return unix.Flistxattr(fd, dest)
	})
	if err != nil {
		return nil, err
	}
	return parseNulXattrList(raw), nil
}
