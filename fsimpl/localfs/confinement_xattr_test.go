//go:build linux || darwin || freebsd || netbsd

package localfs

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/hugelgupf/p9/p9"
	"golang.org/x/sys/unix"
)

func requireXattrSupport(t *testing.T, dir string) {
	t.Helper()
	probe := filepath.Join(dir, ".xattr-probe")
	if err := os.WriteFile(probe, nil, 0644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(probe)
	err := unix.Setxattr(probe, "user.p9probe", []byte("1"), 0)
	if errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EPERM) || errors.Is(err, unix.EROFS) {
		t.Skipf("filesystem does not support user xattrs: %v", err)
	}
	if err != nil {
		t.Fatalf("xattr probe failed unexpectedly: %v", err)
	}
}

func assertHostXattr(t *testing.T, path, attr, want string) {
	t.Helper()
	buf := make([]byte, 64)
	n, err := unix.Getxattr(path, attr, buf)
	if err != nil {
		t.Errorf("Getxattr(%s, %s) failed: %v", path, attr, err)
		return
	}
	if string(buf[:n]) != want {
		t.Errorf("Getxattr(%s, %s) = %q, want %q", path, attr, string(buf[:n]), want)
	}
}

func TestXattrNoFollowAndConfinement(t *testing.T) {
	parent := t.TempDir()
	requireXattrSupport(t, parent)

	outside := filepath.Join(parent, "outside")
	export := filepath.Join(parent, "export")
	sibling := filepath.Join(export, "sibling")
	dPath := filepath.Join(export, "d")
	for _, d := range []string{outside, sibling, dPath} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	outsideFile := filepath.Join(outside, "target")
	siblingFile := filepath.Join(sibling, "target")
	regularFile := filepath.Join(export, "regular")
	for _, p := range []string{outsideFile, siblingFile, regularFile} {
		if err := os.WriteFile(p, []byte("data"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dPath, "target"), []byte("orig"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := unix.Setxattr(outsideFile, "user.secret", []byte("out-val"), 0); err != nil {
		t.Fatal(err)
	}
	if err := unix.Setxattr(siblingFile, "user.secret", []byte("in-val"), 0); err != nil {
		t.Fatal(err)
	}

	mustSymlink(t, outsideFile, filepath.Join(export, "sym_out"))
	mustSymlink(t, filepath.Join("sibling", "target"), filepath.Join(export, "sym_in"))

	root, err := Attacher(export).Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	// Normal xattr operations on a regular file (including empty list and
	// empty value).
	_, reg, err := root.Walk([]string{"regular"})
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()

	list, err := reg.ListXattrs()
	if err != nil {
		t.Fatalf("ListXattrs(empty) = %v", err)
	}
	if len(list) != 0 {
		t.Errorf("ListXattrs(empty) = %v, want empty", list)
	}
	if err := reg.SetXattr("user.k1", []byte("v1"), 0); err != nil {
		t.Fatalf("SetXattr(user.k1) = %v", err)
	}
	if err := reg.SetXattr("user.empty", []byte{}, 0); err != nil {
		t.Fatalf("SetXattr(user.empty) = %v", err)
	}
	if got, err := reg.GetXattr("user.k1"); err != nil || string(got) != "v1" {
		t.Fatalf("GetXattr(user.k1) = (%q, %v), want (v1, nil)", string(got), err)
	}
	if got, err := reg.GetXattr("user.empty"); err != nil || len(got) != 0 {
		t.Fatalf("GetXattr(user.empty) = (%q, %v), want (empty, nil)", string(got), err)
	}
	list, err = reg.ListXattrs()
	if err != nil {
		t.Fatalf("ListXattrs = %v", err)
	}
	slices.Sort(list)
	if !slices.Equal(list, []string{"user.empty", "user.k1"}) {
		t.Errorf("ListXattrs = %v, want [user.empty user.k1]", list)
	}
	if err := reg.RemoveXattr("user.k1"); err != nil {
		t.Fatalf("RemoveXattr(user.k1) = %v", err)
	}

	// Normal xattr on export root and nested subdirectory file (O_PATH
	// parent fd on Linux).
	if err := root.SetXattr("user.rootattr", []byte("ok"), 0); err != nil {
		t.Fatalf("root.SetXattr = %v", err)
	}
	if got, err := root.GetXattr("user.rootattr"); err != nil || string(got) != "ok" {
		t.Fatalf("root.GetXattr = (%q, %v), want (ok, nil)", string(got), err)
	}
	_, nestedFile, err := root.Walk([]string{"sibling", "target"})
	if err != nil {
		t.Fatal(err)
	}
	defer nestedFile.Close()
	if got, err := nestedFile.GetXattr("user.secret"); err != nil || string(got) != "in-val" {
		t.Fatalf("nestedFile.GetXattr = (%q, %v), want (in-val, nil)", string(got), err)
	}
	if err := nestedFile.SetXattr("user.nested", []byte("nv"), 0); err != nil {
		t.Fatalf("nestedFile.SetXattr = %v", err)
	}
	if err := nestedFile.RemoveXattr("user.nested"); err != nil {
		t.Fatalf("nestedFile.RemoveXattr = %v", err)
	}

	// Leaf symlinks must never expose or modify target xattrs.
	for _, tc := range []struct {
		link   string
		target string
		want   string
	}{
		{"sym_out", outsideFile, "out-val"},
		{"sym_in", siblingFile, "in-val"},
	} {
		_, sym, err := root.Walk([]string{tc.link})
		if err != nil {
			t.Fatal(err)
		}
		if got, err := sym.GetXattr("user.secret"); err == nil {
			t.Errorf("%s.GetXattr(user.secret) = %q, want error", tc.link, string(got))
		}
		if gotList, err := sym.ListXattrs(); err == nil && slices.Contains(gotList, "user.secret") {
			t.Errorf("%s.ListXattrs() leaked target xattr: %v", tc.link, gotList)
		}
		if err := sym.SetXattr("user.secret", []byte("pwned"), 0); err == nil {
			t.Errorf("%s.SetXattr succeeded, want error", tc.link)
		}
		if err := sym.RemoveXattr("user.secret"); err == nil {
			t.Errorf("%s.RemoveXattr succeeded, want error", tc.link)
		}
		sym.Close()

		assertHostXattr(t, tc.target, "user.secret", tc.want)
	}

	// Stale fid after intermediate directory replaced by symlink must fail
	// all xattr ops without touching outsideFile.
	_, stale, err := root.Walk([]string{"d", "target"})
	if err != nil {
		t.Fatal(err)
	}
	defer stale.Close()
	if err := os.RemoveAll(dPath); err != nil {
		t.Fatal(err)
	}
	mustSymlink(t, outside, dPath)

	if got, err := stale.GetXattr("user.secret"); err == nil {
		t.Errorf("stale.GetXattr = %q, want error", string(got))
	}
	if gotList, err := stale.ListXattrs(); err == nil {
		t.Errorf("stale.ListXattrs = %v, want error", gotList)
	}
	if err := stale.SetXattr("user.secret", []byte("pwned"), 0); err == nil {
		t.Error("stale.SetXattr succeeded, want error")
	}
	if err := stale.RemoveXattr("user.secret"); err == nil {
		t.Error("stale.RemoveXattr succeeded, want error")
	}

	assertHostXattr(t, outsideFile, "user.secret", "out-val")

	// Opened-then-unlinked file xattr operations must continue working via
	// the open file descriptor.
	_, opened, err := root.Walk([]string{"regular"})
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if _, _, err := opened.Open(p9.ReadWrite); err != nil {
		t.Fatal(err)
	}
	if err := root.UnlinkAt("regular", 0); err != nil {
		t.Fatal(err)
	}
	if err := opened.SetXattr("user.unlinked", []byte("still-here"), 0); err != nil {
		t.Fatalf("SetXattr on unlinked open file = %v", err)
	}
	if got, err := opened.GetXattr("user.unlinked"); err != nil || string(got) != "still-here" {
		t.Fatalf("GetXattr on unlinked open file = (%q, %v), want (still-here, nil)", string(got), err)
	}
}

func TestReadXattrBytesGrowthRetry(t *testing.T) {
	// Simulate FreeBSD/NetBSD extattr silent truncation when a value grows
	// between the size probe and read.
	val := []byte("short")
	grown := []byte("much-longer-value")
	probes := 0
	got, err := readXattrBytes(func(dest []byte) (int, error) {
		if dest == nil {
			probes++
			if probes == 1 {
				return len(val), nil
			}
			return len(grown), nil
		}
		// Value grew to grown before the read.
		n := copy(dest, grown)
		return n, nil
	})
	if err != nil {
		t.Fatalf("readXattrBytes(silent-truncate) = %v", err)
	}
	if string(got) != string(grown) || probes != 2 {
		t.Errorf("readXattrBytes = (%q, probes=%d), want (%q, 2)", string(got), probes, string(grown))
	}

	// Simulate Linux/Darwin ERANGE retry on growth.
	erangeProbes := 0
	got, err = readXattrBytes(func(dest []byte) (int, error) {
		if dest == nil {
			erangeProbes++
			if erangeProbes == 1 {
				return len(val), nil
			}
			return len(grown), nil
		}
		if len(dest) < len(grown) {
			return 0, unix.ERANGE
		}
		return copy(dest, grown), nil
	})
	if err != nil {
		t.Fatalf("readXattrBytes(ERANGE) = %v", err)
	}
	if string(got) != string(grown) || erangeProbes != 2 {
		t.Errorf("readXattrBytes(ERANGE) = (%q, probes=%d), want (%q, 2)", string(got), erangeProbes, string(grown))
	}
}
