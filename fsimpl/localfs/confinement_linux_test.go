//go:build linux

package localfs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hugelgupf/p9/linux"
	"github.com/hugelgupf/p9/p9"
	"golang.org/x/sys/unix"
)

func TestOpenat2Fallback(t *testing.T) {
	prev := openat2Unsupported.Swap(true)
	t.Cleanup(func() {
		openat2Unsupported.Store(prev)
	})
	TestStaleFIDIntermediateSymlinkReplacement(t)
	TestNestedDirectoryOperations(t)
}

func TestOpenFlagsAllowlist(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(dir, "sym")); err != nil {
		t.Fatal(err)
	}

	root, err := Attacher(dir).Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	// O_PATH in OpenFlags must be stripped so O_NOFOLLOW still rejects
	// symlinks instead of opening an O_PATH symlink fd.
	_, sym, err := root.Walk([]string{"sym"})
	if err != nil {
		t.Fatal(err)
	}
	defer sym.Close()
	if _, _, err := sym.Open(p9.ReadOnly | p9.OpenFlags(unix.O_PATH)); !errors.Is(err, unix.ELOOP) {
		t.Errorf("Open(sym, ReadOnly|O_PATH) = %v, want ELOOP", err)
	}

	// O_PATH in Create must be stripped so O_CREAT|O_EXCL fails with
	// EEXIST on an existing symlink.
	if f, _, _, err := root.Create("sym", p9.ReadWrite|p9.OpenFlags(unix.O_PATH), 0644, p9.NoUID, p9.NoGID); !errors.Is(err, unix.EEXIST) {
		if f != nil {
			f.Close()
		}
		t.Errorf("Create(sym, ReadWrite|O_PATH) = %v, want EEXIST", err)
	}

	// Access mode 3 (neither RDONLY, WRONLY, nor RDWR) must be rejected.
	_, reg, err := root.Walk([]string{"target"})
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	if _, _, err := reg.Open(p9.OpenFlagsModeMask); !errors.Is(err, linux.EINVAL) {
		t.Errorf("Open(target, accessMode=3) = %v, want EINVAL", err)
	}
}

func TestFchmodAtNoFollowFallback(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	reg := filepath.Join(sub, "reg")
	if err := os.WriteFile(reg, []byte("x"), 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(reg, 0600) })
	target := filepath.Join(sub, "target")
	if err := os.WriteFile(target, []byte("y"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(sub, "sym")); err != nil {
		t.Fatal(err)
	}

	// Use an O_PATH parent directory descriptor to match openParent().
	root, err := Attacher(dir).Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	_, subFid, err := root.Walk([]string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	defer subFid.Close()

	dirFD, release, err := subFid.(*Local).openDir()
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	// Fallback must chmod even a 0000 file via O_PATH + /proc/self/fd.
	if err := fchmodAtNoFollowFallback(dirFD, "reg", 0640); err != nil {
		t.Fatalf("fchmodAtNoFollowFallback(reg) = %v", err)
	}
	fi, err := os.Lstat(reg)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0640 {
		t.Errorf("reg perm = %#o, want 0640", got)
	}

	// Fallback must reject a symlink with EOPNOTSUPP and leave target
	// intact.
	if err := fchmodAtNoFollowFallback(dirFD, "sym", 0600); !errors.Is(err, unix.EOPNOTSUPP) {
		t.Errorf("fchmodAtNoFollowFallback(sym) = %v, want EOPNOTSUPP", err)
	}
	ti, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := ti.Mode().Perm(); got != 0644 {
		t.Errorf("target perm changed = %#o, want 0644", got)
	}
}

func TestWalkThroughExecuteOnlyDir(t *testing.T) {
	dir := t.TempDir()
	execDir := filepath.Join(dir, "exec_only")
	if err := os.MkdirAll(filepath.Join(execDir, "inner"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(execDir, "inner", "f"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(execDir, 0111); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(execDir, 0755) })

	root, err := Attacher(dir).Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	// Execute-only (0111) intermediate directories remain traversable on
	// Linux thanks to O_PATH.
	_, ef, err := root.Walk([]string{"exec_only", "inner", "f"})
	if err != nil {
		t.Fatalf("Walk through 0111 dir = %v", err)
	}
	defer ef.Close()
	if _, _, err := ef.Open(p9.ReadOnly); err != nil {
		t.Fatalf("Open through 0111 dir = %v", err)
	}
}

func TestWithPinnedAtProcAbsentFallback(t *testing.T) {
	dir := t.TempDir()
	reg := filepath.Join(dir, "reg")
	if err := os.WriteFile(reg, []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("reg", filepath.Join(dir, "sym")); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(dir, "pipe"), 0644); err != nil {
		t.Fatal(err)
	}

	root, err := Attacher(dir).Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	dirFD, release, err := root.(*Local).openDir()
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	noProc := func(string) error { return unix.ENOENT }

	// Regular file must invoke fdFn with a valid fd.
	called := false
	if err := withPinnedAt(dirFD, "reg", noProc, func(fd int) error {
		called = true
		var st unix.Stat_t
		return fstat(fd, &st)
	}); err != nil || !called {
		t.Errorf("withPinnedAt(reg) = (%v, called=%v), want (nil, true)", err, called)
	}

	// Symlink must fail with ELOOP without invoking fdFn.
	if err := withPinnedAt(dirFD, "sym", noProc, func(int) error {
		t.Error("fdFn invoked for symlink")
		return nil
	}); !errors.Is(err, unix.ELOOP) {
		t.Errorf("withPinnedAt(sym) = %v, want ELOOP", err)
	}

	// FIFO must fail with EOPNOTSUPP without blocking or invoking fdFn.
	if err := withPinnedAt(dirFD, "pipe", noProc, func(int) error {
		t.Error("fdFn invoked for FIFO")
		return nil
	}); !errors.Is(err, unix.EOPNOTSUPP) {
		t.Errorf("withPinnedAt(pipe) = %v, want EOPNOTSUPP", err)
	}
}

func TestPrepareOpenProcAbsentFallback(t *testing.T) {
	dir := t.TempDir()
	reg := filepath.Join(dir, "reg")
	if err := os.WriteFile(reg, []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other"), []byte("other"), 0644); err != nil {
		t.Fatal(err)
	}

	root, err := Attacher(dir).Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	absentProc := func(int) string {
		return filepath.Join(dir, "nonexistent-proc-fd")
	}

	// Normal open succeeds through the /proc-absent fallback.
	_, f1, err := root.Walk([]string{"reg"})
	if err != nil {
		t.Fatal(err)
	}
	defer f1.Close()
	finish, err := f1.(*Local).prepareOpenWithProc(p9.ReadOnly, absentProc)
	if err != nil {
		t.Fatalf("prepareOpenWithProc(reg) = %v", err)
	}
	if _, _, err := finish(); err != nil {
		t.Fatalf("finish(reg) = %v", err)
	}

	// Replacing the target with another file before finish must fail with
	// ESTALE without applying O_TRUNC to the replacement file.
	_, f2, err := root.Walk([]string{"reg"})
	if err != nil {
		t.Fatal(err)
	}
	defer f2.Close()
	finish2, err := f2.(*Local).prepareOpenWithProc(p9.WriteOnly|p9.OpenFlags(os.O_TRUNC), absentProc)
	if err != nil {
		t.Fatalf("prepareOpenWithProc(reg2) = %v", err)
	}
	if err := os.Rename(filepath.Join(dir, "other"), reg); err != nil {
		t.Fatal(err)
	}
	if _, _, err := finish2(); !errors.Is(err, unix.ESTALE) {
		t.Errorf("finish(replaced) = %v, want ESTALE", err)
	}
	got, err := os.ReadFile(reg)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "other" {
		t.Errorf("replacement file modified by O_TRUNC: got %q, want %q", string(got), "other")
	}

	// Replacing a regular file with a peerless FIFO before finish must fail
	// promptly with ESTALE instead of blocking in open(2).
	_, f3, err := root.Walk([]string{"reg"})
	if err != nil {
		t.Fatal(err)
	}
	defer f3.Close()
	finish3, err := f3.(*Local).prepareOpenWithProc(p9.ReadOnly, absentProc)
	if err != nil {
		t.Fatalf("prepareOpenWithProc(reg3) = %v", err)
	}
	if err := os.Remove(reg); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(reg, 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := finish3(); !errors.Is(err, unix.ESTALE) {
		t.Errorf("finish(replaced by FIFO) = %v, want ESTALE", err)
	}

	// Opening a directory with ReadOnly|O_TRUNC via the /proc-absent
	// fallback must fail with EISDIR.
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	_, f4, err := root.Walk([]string{"sub"})
	if err != nil {
		t.Fatal(err)
	}
	defer f4.Close()
	finish4, err := f4.(*Local).prepareOpenWithProc(p9.ReadOnly|p9.OpenFlags(os.O_TRUNC), absentProc)
	if err != nil {
		t.Fatalf("prepareOpenWithProc(sub) = %v", err)
	}
	if _, _, err := finish4(); !errors.Is(err, unix.EISDIR) {
		t.Errorf("finish(dir, ReadOnly|O_TRUNC) = %v, want EISDIR", err)
	}
}
