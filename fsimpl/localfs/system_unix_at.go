//go:build !windows && !solaris

package localfs

import "golang.org/x/sys/unix"

func (l *Local) symlink(oldname, newname string) error {
	dirFD, release, err := l.openDir()
	if err != nil {
		return err
	}
	defer release()

	return ignoringEINTR(func() error {
		return unix.Symlinkat(oldname, dirFD, newname)
	})
}

func (l *Local) link(target *Local, newname string) error {
	targetParentFD, releaseTarget, targetBase, err := target.openParent()
	if err != nil {
		return err
	}
	defer releaseTarget()

	destDirFD, releaseDest, err := l.openDir()
	if err != nil {
		return err
	}
	defer releaseDest()

	// Flags == 0 does not set AT_SYMLINK_FOLLOW, so leaf symlinks are
	// never followed even on platforms (such as macOS) where link(2) does.
	return ignoringEINTR(func() error {
		return unix.Linkat(targetParentFD, targetBase, destDirFD, newname, 0)
	})
}

func (l *Local) readlink() (string, error) {
	parentFD, release, base, err := l.openParent()
	if err != nil {
		return "", err
	}
	defer release()

	for size := 128; ; size *= 2 {
		buf := make([]byte, size)
		n, err := ignoringEINTRInt(func() (int, error) {
			return unix.Readlinkat(parentFD, base, buf)
		})
		if err == unix.ERANGE {
			continue
		}
		if err != nil {
			return "", err
		}
		if n < len(buf) {
			return string(buf[:n]), nil
		}
	}
}
