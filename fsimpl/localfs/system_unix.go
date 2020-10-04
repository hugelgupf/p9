// +build !windows,!plan9

package localfs

import (
	"os"
	"syscall"
)

func umask(mask int) int {
	return syscall.Umask(mask)
}

func localToQid(_ string, fi os.FileInfo) (uint64, error) {
	return uint64(fi.Sys().(*syscall.Stat_t).Ino), nil
}
