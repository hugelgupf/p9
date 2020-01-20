// +build linux

package sys

import (
	"syscall"

	"github.com/hugelgupf/p9/sys/linux"
)

func sysErrno(err error) linux.Errno {
	se, ok := err.(syscall.Errno)
	if ok {
		return linux.Errno(se)
	}
	return 0
}
