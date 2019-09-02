// +build freebsd dragonfly openbsd

package sys

import "golang.org/x/sys/unix"

// Errno definitions.
const (
	ENODATA = unix.ENOATTR
)
