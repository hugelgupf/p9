//go:build solaris

package localfs

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// Solaris's golang.org/x/sys/unix lacks Symlinkat, Linkat, and Readlinkat.
// Verify that intermediate directories contain no symlinks via openDir /
// openParent and that os.OpenRoot resolves to the same pinned (dev, ino)
// directory before calling os.Root's Symlink, Link, and Readlink.

func (r *rootDir) openVerifiedRoot() (*os.Root, error) {
	root, err := os.OpenRoot(r.path)
	if err != nil {
		return nil, err
	}
	fi, err := root.Stat(".")
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || (devino{dev: uint64(st.Dev), ino: st.Ino} != r.id) {
		_ = root.Close()
		return nil, unix.ESTALE
	}
	return root, nil
}

func (l *Local) symlink(oldname, newname string) error {
	_, release, err := l.openDir()
	if err != nil {
		return err
	}
	defer release()

	r, err := l.root.openVerifiedRoot()
	if err != nil {
		return err
	}
	defer r.Close()
	return r.Symlink(oldname, joinRel(l.rel, newname))
}

func (l *Local) link(target *Local, newname string) error {
	_, releaseTarget, _, err := target.openParent()
	if err != nil {
		return err
	}
	defer releaseTarget()

	_, releaseDest, err := l.openDir()
	if err != nil {
		return err
	}
	defer releaseDest()

	r, err := l.root.openVerifiedRoot()
	if err != nil {
		return err
	}
	defer r.Close()
	return r.Link(target.rel, joinRel(l.rel, newname))
}

func (l *Local) readlink() (string, error) {
	_, release, _, err := l.openParent()
	if err != nil {
		return "", err
	}
	defer release()

	r, err := l.root.openVerifiedRoot()
	if err != nil {
		return "", err
	}
	defer r.Close()
	return r.Readlink(l.rel)
}
