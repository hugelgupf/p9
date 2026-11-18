//go:build unix && !solaris && !openbsd

package localfs

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/hugelgupf/p9/fsimpl/xattr"
	"github.com/hugelgupf/p9/p9"
	"golang.org/x/sys/unix"
)

func requireXattrSupport(t *testing.T, dir string) {
	t.Helper()
	probe := filepath.Join(dir, ".xattr-probe")
	if err := os.WriteFile(probe, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(probe)
	if err := unix.Lsetxattr(probe, "user.probe", []byte("1"), 0); err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
			t.Skipf("filesystem at %s does not support user xattrs: %v", dir, err)
		}
		t.Fatalf("Lsetxattr probe: %v", err)
	}
}

func serveLocalXattrFS(t *testing.T, dir string) p9.File {
	t.Helper()
	srv, cli := net.Pipe()
	s := p9.NewServer(Attacher(dir))
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Handle(srv, srv)
	}()
	t.Cleanup(func() {
		_ = cli.Close()
		_ = srv.Close()
		<-done
	})

	c, err := p9.NewClient(cli)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	root, err := c.Attach("")
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root
}

func filterUserAttrs(names []string) []string {
	var out []string
	for _, name := range names {
		if strings.HasPrefix(name, "user.") {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

func TestOpenFileSurvivesXattrWalk(t *testing.T) {
	dir := t.TempDir()
	requireXattrSupport(t, dir)

	hostPath := filepath.Join(dir, "openfile")
	if err := os.WriteFile(hostPath, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Lsetxattr(hostPath, "user.meta", []byte("attrval"), 0); err != nil {
		t.Fatal(err)
	}

	root := serveLocalXattrFS(t, dir)

	_, f, err := root.Walk([]string{"openfile"})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	defer f.Close()

	if _, _, err := f.Open(p9.ReadWrite); err != nil {
		t.Fatalf("Open: %v", err)
	}

	gotAttr, err := f.GetXattr("user.meta")
	if err != nil {
		t.Fatalf("GetXattr: %v", err)
	}
	if string(gotAttr) != "attrval" {
		t.Fatalf("GetXattr = %q, want %q", gotAttr, "attrval")
	}

	names, err := f.ListXattrs()
	if err != nil {
		t.Fatalf("ListXattrs: %v", err)
	}
	if slices.Contains(names, "") {
		t.Fatalf("ListXattrs = %q, contains empty name", names)
	}
	if gotUser := filterUserAttrs(names); !slices.Equal(gotUser, []string{"user.meta"}) {
		t.Fatalf("filterUserAttrs(ListXattrs) = %q, want [user.meta]", gotUser)
	}

	// Reading, writing, and syncing the open file must still work after the
	// transient xattr walk FIDs have been clunked.
	if n, err := f.WriteAt([]byte(" world"), 5); err != nil || n != 6 {
		t.Fatalf("WriteAt after xattr walk = (%d, %v), want (6, nil)", n, err)
	}
	if err := f.FSync(); err != nil {
		t.Fatalf("FSync after xattr walk = %v, want nil", err)
	}
	buf := make([]byte, 32)
	n, err := f.ReadAt(buf, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("ReadAt after xattr walk = %v", err)
	}
	if string(buf[:n]) != "hello world" {
		t.Fatalf("ReadAt = %q, want %q", buf[:n], "hello world")
	}
}

func TestLocalSymlinkXattrNoFollow(t *testing.T) {
	dir := t.TempDir()
	requireXattrSupport(t, dir)

	outsideDir := t.TempDir()
	requireXattrSupport(t, outsideDir)
	target := filepath.Join(outsideDir, "secret")
	if err := os.WriteFile(target, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Lsetxattr(target, "user.secret", []byte("keepme"), 0); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(target, filepath.Join(dir, "sym")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outsideDir, "missing"), filepath.Join(dir, "dangling")); err != nil {
		t.Fatal(err)
	}

	root, err := Attacher(dir).Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	_, sym, err := root.Walk([]string{"sym"})
	if err != nil {
		t.Fatal(err)
	}
	defer sym.Close()

	_, dangling, err := root.Walk([]string{"dangling"})
	if err != nil {
		t.Fatal(err)
	}
	defer dangling.Close()

	gotSym, err := sym.ListXattrs()
	if err != nil || slices.Contains(gotSym, "") || slices.Contains(gotSym, "user.secret") {
		t.Errorf("sym.ListXattrs() = (%q, %v), want no empty/target attrs", gotSym, err)
	}
	gotDangling, err := dangling.ListXattrs()
	if err != nil || slices.Contains(gotDangling, "") {
		t.Errorf("dangling.ListXattrs() = (%q, %v), want no error or empty attr", gotDangling, err)
	}
	if got, err := sym.GetXattr("user.secret"); err == nil {
		t.Errorf("sym.GetXattr(user.secret) = (%q, nil), want error", got)
	}

	// Test RemoveXattr before SetXattr because macOS/BSD allow setting
	// user.* xattrs directly on symlink inodes with XATTR_NOFOLLOW.
	if err := sym.RemoveXattr("user.secret"); err == nil {
		t.Errorf("sym.RemoveXattr(user.secret) = nil, want error")
	}
	if got, err := xattr.Get(target, "user.secret"); err != nil || string(got) != "keepme" {
		t.Fatalf("target user.secret after sym.RemoveXattr = (%q, %v), want (\"keepme\", nil)", got, err)
	}

	// Attempting SetXattr on sym must never overwrite target's xattr
	// (Linux rejects with EPERM; macOS/BSD set it on sym itself).
	err = sym.SetXattr("user.secret", []byte("overwritten"), 0)
	if runtime.GOOS == "linux" && err == nil {
		t.Errorf("sym.SetXattr(user.secret) on linux = nil, want EPERM")
	}
	if got, err := xattr.Get(target, "user.secret"); err != nil || string(got) != "keepme" {
		t.Fatalf("target user.secret after sym.SetXattr = (%q, %v), want (\"keepme\", nil)", got, err)
	}
}
