// +build plan9

package internal

import (
	"syscall"

	"github.com/hugelgupf/p9/internal/linux"
)

func sysErrno(err error) linux.Errno {
	switch err {
	case syscall.ENOENT:
		return linux.ENOENT
	case syscall.EACCES:
		return linux.EACCES
	case syscall.EEXIST:
		return linux.EEXIST
	default:
		// No clue what to do with others.
		return 0
	}
}
