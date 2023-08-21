//go:build unix

package composefs

import (
	"golang.org/x/sys/unix"
)

func setUmask() {
	unix.Umask(0)
}
