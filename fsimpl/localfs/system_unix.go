// +build !windows

package localfs

import (
	"os"
	"syscall"
)

func localToQid(_ string, fi os.FileInfo) (uint64, error) {
	return uint64(fi.Sys().(*syscall.Stat_t).Ino), nil
}
