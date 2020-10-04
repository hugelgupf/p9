package localfs

import (
	"os"
	"syscall"
)

func umask(_ int) int {
	return 0
}

func localToQid(path string, info os.FileInfo) (uint64, error) {
	a := info.Sys().(*syscall.Dir)
	x := uint64(a.Qid.Path)
	return x, nil
}
