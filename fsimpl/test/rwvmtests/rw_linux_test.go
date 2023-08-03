// Package rwvmtests_test uses the Linux kernel client to mount a 9P file
// system for reading and writing and performs tests on that file system.
package rwvmtests_test

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/u-root/u-root/pkg/mount"
	"github.com/u-root/u-root/pkg/sh"
	"github.com/u-root/u-root/pkg/testutil"
	"golang.org/x/sys/unix"
)

func TestMain(m *testing.M) {
	if os.Getuid() == 0 {
		if err := sh.RunWithLogs("dhclient", "-ipv6=false"); err != nil {
			log.Fatalf("could not configure network for tests: %v", err)
		}
	}

	os.Exit(m.Run())
}

func TestMountP9(t *testing.T) {
	testutil.SkipIfNotRoot(t)

	targetDir := "/target"
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(targetDir)

	mp, err := mount.Mount(os.Getenv("P9_TARGET"), targetDir, "9p", fmt.Sprintf("trans=tcp,port=%s", os.Getenv("P9_PORT")), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer mp.Unmount(0)

	t.Run("write-read-stat", func(t *testing.T) {
		want := "haha"
		if err := ioutil.WriteFile(filepath.Join(targetDir, "foobar"), []byte(want), 0755); err != nil {
			t.Fatal(err)
		}

		content, err := ioutil.ReadFile("/target/foobar")
		if err != nil {
			t.Error(err)
		}
		if got := string(content); got != want {
			t.Errorf("content of /target/foobar is %v, want %v", got, want)
		}

		fi, err := os.Stat("/target/foobar")
		if err != nil {
			t.Error(err)
		}
		if got, want := fi.Mode().Perm(), os.FileMode(0755); got != want {
			t.Errorf("permissions of /target/foobar are %s, want %s", got, want)
		}

		if err := sh.RunWithLogs("ls", "-lh", "/target"); err != nil {
			t.Error(err)
		}
	})

	t.Run("xattr-list", func(t *testing.T) {
		p := filepath.Join(targetDir, "xattrlist")
		if err := ioutil.WriteFile(p, []byte("somecontent"), 0755); err != nil {
			t.Fatal(err)
		}

		if err := unix.Setxattr(p, "user.p9.test", []byte("y"), 0); err != nil {
			t.Fatalf("Setxattr: %v", err)
		}
		if err := unix.Setxattr(p, "user.p9.test2", []byte("y"), 0); err != nil {
			t.Fatalf("Setxattr: %v", err)
		}

		sz, err := unix.Listxattr(p, nil)
		if err != nil {
			t.Fatalf("Listxattr(nil) = %v", err)
		}

		b := make([]byte, sz)
		sz, err = unix.Listxattr(p, b)
		if err != nil {
			t.Fatalf("Listxattr() = %v", err)
		}

		xattrs := strings.Split(strings.Trim(string(b[:sz]), "\000"), "\000")
		t.Logf("Xattrs: %v", xattrs)

		want := []string{
			"user.p9.test",
			"user.p9.test2",
		}
		if !reflect.DeepEqual(xattrs, want) {
			t.Errorf("Listxattr = %v, want %v", xattrs, want)
		}
	})

	t.Run("xattr-set", func(t *testing.T) {
		p := filepath.Join(targetDir, "xattrcreate")
		if err := ioutil.WriteFile(p, []byte("somecontent"), 0755); err != nil {
			t.Fatal(err)
		}

		// No flag can create an attribute.
		if err := unix.Setxattr(p, "user.p9.test", []byte("y"), 0); err != nil {
			t.Fatalf("Setxattr = %v", err)
		}

		// XATTR_CREATE fails if the attribute already exists.
		if err := unix.Setxattr(p, "user.p9.test", []byte("n"), unix.XATTR_CREATE); !errors.Is(err, unix.EEXIST) {
			t.Fatalf("Setxattr = %v, want EEXIST", err)
		}

		// XATTR_REPLACE will replace the attribute + value.
		if err := unix.Setxattr(p, "user.p9.test", []byte("n"), unix.XATTR_REPLACE); err != nil {
			t.Fatalf("Setxattr = %v", err)
		}

		// XATTR_REPLACE must operate on an existing attribute, or it fails.
		if err := unix.Setxattr(p, "user.p9.doesnotexist", []byte("n"), unix.XATTR_REPLACE); !errors.Is(err, unix.ENODATA) {
			t.Fatalf("Setxattr = %v", err)
		}

		// No flag can replace an existing attribute.
		if err := unix.Setxattr(p, "user.p9.test", []byte("y"), 0); err != nil {
			t.Fatalf("Setxattr = %v", err)
		}
	})

	t.Run("xattr-get", func(t *testing.T) {
		p := filepath.Join(targetDir, "xattrget")
		if err := ioutil.WriteFile(p, []byte("somecontent"), 0755); err != nil {
			t.Fatal(err)
		}

		if err := unix.Setxattr(p, "user.p9.test", []byte("y"), 0); err != nil {
			t.Fatalf("Setxattr = %v", err)
		}

		sz, err := unix.Getxattr(p, "user.p9.test", nil)
		if err != nil {
			t.Errorf("Getxattr() = %v", err)
		}

		b := make([]byte, sz)
		sz, err = unix.Getxattr(p, "user.p9.test", b)
		if err != nil {
			t.Fatalf("Getxattr() = %v", err)
		}

		if got := string(b[:sz]); got != "y" {
			t.Errorf("Getxattr = %v, want y", got)
		}
	})

	if err := mp.Unmount(0); err != nil {
		t.Fatal(err)
	}
}
