//go:build !windows

package localfs

func isWindowsSymlinkPrivilegeErr(error) bool {
	return false
}
