//go:build windows
// +build windows

package linux

import "syscall"

func sysErrno(err error) Errno {
	switch err {
	case syscall.ERROR_FILE_NOT_FOUND:
		return ENOENT
	case syscall.ERROR_PATH_NOT_FOUND:
		return ENOENT
	case syscall.ERROR_ACCESS_DENIED:
		return EACCES
	case syscall.ERROR_FILE_EXISTS:
		return EEXIST
	case syscall.ERROR_INSUFFICIENT_BUFFER:
		return ENOMEM
	default:
		// No clue what to do with others.
		return 0
	}
}
