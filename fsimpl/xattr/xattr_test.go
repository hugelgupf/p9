//go:build unix && !openbsd && !solaris

package xattr

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

func requireUserXattrSupport(t *testing.T, dir string) {
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

func userXattrs(names []string) []string {
	var out []string
	for _, name := range names {
		if strings.HasPrefix(name, "user.") {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

func TestListEmptyAndNonEmpty(t *testing.T) {
	// Deterministic check for a 0-byte list response, independent of any
	// ambient OS xattrs such as com.apple.provenance or security.selinux.
	gotZero, err := listWith("/fake", func(dest []byte) (int, error) {
		return 0, nil
	})
	if err != nil || len(gotZero) != 0 {
		t.Fatalf("listWith(0 bytes) = (%q, %v), want (nil, nil)", gotZero, err)
	}

	dir := t.TempDir()
	requireUserXattrSupport(t, dir)

	emptyFile := filepath.Join(dir, "empty")
	if err := os.WriteFile(emptyFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := List(emptyFile)
	if err != nil {
		t.Fatalf("List(no user xattrs) error = %v, want nil", err)
	}
	if slices.Contains(got, "") {
		t.Fatalf("List(no user xattrs) = %q, contains empty name", got)
	}
	if sz, err := unix.Llistxattr(emptyFile, nil); err == nil && sz == 0 && len(got) != 0 {
		t.Fatalf("List(0-byte xattrs) = %q, want empty slice", got)
	}
	if gotUser := userXattrs(got); len(gotUser) != 0 {
		t.Fatalf("userXattrs(emptyFile) = %q, want empty", gotUser)
	}

	withAttrs := filepath.Join(dir, "attrs")
	if err := os.WriteFile(withAttrs, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Lsetxattr(withAttrs, "user.zero", []byte{}, 0); err != nil {
		t.Fatal(err)
	}
	if err := unix.Lsetxattr(withAttrs, "user.hello", []byte("world"), 0); err != nil {
		t.Fatal(err)
	}

	valZero, err := Get(withAttrs, "user.zero")
	if err != nil {
		t.Fatalf("Get(user.zero) error = %v", err)
	}
	if len(valZero) != 0 {
		t.Errorf("Get(user.zero) = %q, want empty", valZero)
	}

	valHello, err := Get(withAttrs, "user.hello")
	if err != nil {
		t.Fatalf("Get(user.hello) error = %v", err)
	}
	if string(valHello) != "world" {
		t.Errorf("Get(user.hello) = %q, want %q", valHello, "world")
	}

	got, err = List(withAttrs)
	if err != nil {
		t.Fatalf("List(attrs) error = %v", err)
	}
	if slices.Contains(got, "") {
		t.Errorf("List(attrs) = %q, contains empty name", got)
	}
	want := []string{"user.hello", "user.zero"}
	if gotUser := userXattrs(got); !slices.Equal(gotUser, want) {
		t.Errorf("userXattrs(attrs) = %q, want %q", gotUser, want)
	}
}

func TestSymlinkNoFollow(t *testing.T) {
	dir := t.TempDir()
	requireUserXattrSupport(t, dir)

	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Lsetxattr(target, "user.secret", []byte("topsecret"), 0); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(dir, "dangling")
	if err := os.Symlink(filepath.Join(dir, "does-not-exist"), dangling); err != nil {
		t.Fatal(err)
	}

	// Neither valid nor dangling symlinks should follow to target or fail
	// with ENOENT.
	gotLink, err := List(link)
	if err != nil || slices.Contains(gotLink, "") || slices.Contains(gotLink, "user.secret") {
		t.Errorf("List(symlink) = (%q, %v), want no empty/target attrs", gotLink, err)
	}
	gotDangling, err := List(dangling)
	if err != nil || slices.Contains(gotDangling, "") {
		t.Errorf("List(dangling) = (%q, %v), want no error or empty attr", gotDangling, err)
	}
	if got, err := Get(link, "user.secret"); err == nil {
		t.Errorf("Get(symlink, user.secret) = (%q, nil), want error", got)
	}
	if got, err := Get(dangling, "user.secret"); err == nil || errors.Is(err, unix.ENOENT) {
		t.Errorf("Get(dangling, user.secret) = (%q, %v), want non-ENOENT xattr error", got, err)
	}
}

func TestConcurrentZeroAndGrowthRace(t *testing.T) {
	dir := t.TempDir()
	requireUserXattrSupport(t, dir)

	p := filepath.Join(dir, "racers")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Lsetxattr(p, "user.race", nil, 0); err != nil {
		t.Fatal(err)
	}

	small := []byte("abc")
	large := bytes.Repeat([]byte("z"), 4096)

	stop := make(chan struct{})
	var writerWG sync.WaitGroup
	writerWG.Add(1)
	go func() {
		defer writerWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = unix.Lsetxattr(p, "user.race", nil, 0)
			_ = unix.Lsetxattr(p, "user.race", large, 0)
			_ = unix.Lsetxattr(p, "user.race", small, 0)
			_ = unix.Lsetxattr(p, "user.race", large, 0)
			_ = unix.Lsetxattr(p, "user.extra1", []byte("1"), 0)
			_ = unix.Lsetxattr(p, "user.extra2", []byte("2"), 0)
			_ = unix.Lremovexattr(p, "user.extra1")
			_ = unix.Lremovexattr(p, "user.extra2")
		}
	}()
	defer func() {
		close(stop)
		writerWG.Wait()
	}()

	for range 300 {
		got, err := Get(p, "user.race")
		if err != nil {
			t.Fatalf("Get during concurrent growth failed: %v", err)
		}
		if len(got) != 0 && !bytes.Equal(got, small) && !bytes.Equal(got, large) {
			t.Fatalf("Get returned unexpected %d-byte payload", len(got))
		}

		names, err := List(p)
		if err != nil {
			t.Fatalf("List during concurrent mutation failed: %v", err)
		}
		if !slices.Contains(names, "user.race") {
			t.Fatalf("List = %q, missing user.race", names)
		}
	}
}

func TestReadXattrDeterministicRaces(t *testing.T) {
	t.Run("ZeroSizeProbeNeverPassesEmptyBuffer", func(t *testing.T) {
		calls := 0
		got, err := readXattr("/fake", "getxattr", func(dest []byte) (int, error) {
			calls++
			if dest == nil {
				return 0, nil
			}
			if len(dest) == 0 {
				t.Fatalf("readXattr passed empty non-nil buffer")
			}
			return 0, nil
		})
		if err != nil || len(got) != 0 || calls != 2 {
			t.Fatalf("got (%q, %v) in %d calls, want (empty, nil) in 2 calls", got, err, calls)
		}
	})

	t.Run("ZeroToNonZeroGrowthWithERANGE", func(t *testing.T) {
		payload := []byte("grown-from-zero")
		calls := 0
		got, err := readXattr("/fake", "getxattr", func(dest []byte) (int, error) {
			calls++
			if calls == 1 {
				return 0, nil
			}
			if dest == nil {
				return len(payload), nil
			}
			if len(dest) < len(payload) {
				return 0, unix.ERANGE
			}
			return copy(dest, payload), nil
		})
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("got (%q, %v), want (%q, nil)", got, err, payload)
		}
	})

	t.Run("LinearOneByteGrowthPerAttemptStillDoubles", func(t *testing.T) {
		cur := 1
		got, err := readXattr("/fake", "getxattr", func(dest []byte) (int, error) {
			if dest == nil {
				return cur, nil
			}
			cur++
			if cur <= 64 && len(dest) < cur {
				return 0, unix.ERANGE
			}
			payload := bytes.Repeat([]byte("b"), cur)
			return copy(dest, payload), nil
		})
		if err != nil || len(got) == 0 {
			t.Fatalf("got (%d bytes, %v), want non-empty payload", len(got), err)
		}
	})

	t.Run("OversizedReturnWithoutERANGEDoesNotPanic", func(t *testing.T) {
		payload := []byte("oversized-return")
		calls := 0
		got, err := readXattr("/fake", "getxattr", func(dest []byte) (int, error) {
			calls++
			switch calls {
			case 1:
				return 0, nil
			case 2:
				// Simulate a buggy driver returning the grown
				// length without ERANGE.
				return len(payload), nil
			case 3:
				return len(payload), nil
			default:
				return copy(dest, payload), nil
			}
		})
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("got (%q, %v), want (%q, nil)", got, err, payload)
		}
	})

	t.Run("DynamicZeroProbeExponentialGrowth", func(t *testing.T) {
		payload := bytes.Repeat([]byte("a"), 600)
		got, err := readXattr("/fake", "listxattr", func(dest []byte) (int, error) {
			if dest == nil {
				// Always report 0 on size probes.
				return 0, nil
			}
			if len(dest) < len(payload) {
				return 0, unix.ERANGE
			}
			return copy(dest, payload), nil
		})
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("got len %d err %v, want len %d nil", len(got), err, len(payload))
		}
	})

	t.Run("ExhaustedRetriesReturnsERANGE", func(t *testing.T) {
		_, err := readXattr("/fake", "getxattr", func(dest []byte) (int, error) {
			if dest == nil {
				return 4, nil
			}
			return 0, unix.ERANGE
		})
		if !errors.Is(err, unix.ERANGE) {
			t.Fatalf("err = %v, want ERANGE", err)
		}
	})

	t.Run("NegativeProbeSizeReturnsEIO", func(t *testing.T) {
		_, err := readXattr("/fake", "getxattr", func(dest []byte) (int, error) {
			return -1, nil
		})
		if !errors.Is(err, unix.EIO) {
			t.Fatalf("err = %v, want EIO", err)
		}
	})

	t.Run("ReadErrorAfterProbePropagates", func(t *testing.T) {
		_, err := readXattr("/fake", "getxattr", func(dest []byte) (int, error) {
			if dest == nil {
				return 4, nil
			}
			return 0, unix.EACCES
		})
		if !errors.Is(err, unix.EACCES) {
			t.Fatalf("err = %v, want EACCES", err)
		}
	})
}
