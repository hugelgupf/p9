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
