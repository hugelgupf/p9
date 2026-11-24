package localfs

import (
	"errors"
	"os"
	"syscall"
)

func isWindowsSymlinkPrivilegeErr(err error) bool {
	return os.IsPermission(err) || errors.Is(err, syscall.ERROR_PRIVILEGE_NOT_HELD)
}
