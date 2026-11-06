//go:build !windows

package localfs

import "os"

func openRegular(path string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag, perm)
}

func rename(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}
