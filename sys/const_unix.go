//+build !windows,!freebsd,!dragonfly,!openbsd

package sys

import "golang.org/x/sys/unix"

// Errno definitions.
//
// TODO: these are temporary. Define a library with Linux errnos available on
// every platform.
const (
	ENODATA = unix.ENODATA
)
