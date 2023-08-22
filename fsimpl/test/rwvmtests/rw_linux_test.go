// Package rwvmtests_test uses the Linux kernel client to mount a 9P file
// system for reading and writing and performs tests on that file system.
package rwvmtests_test

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/hugelgupf/p9/fsimpl/localfs"
	"github.com/hugelgupf/p9/fsimpl/xattr"
	"github.com/hugelgupf/p9/p9"
	"github.com/u-root/u-root/pkg/mount"
	"github.com/u-root/u-root/pkg/sh"
	"github.com/u-root/u-root/pkg/testutil"
	"github.com/u-root/uio/ulog/ulogtest"
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

func TestMountHostDirectory(t *testing.T) {
	testutil.SkipIfNotRoot(t)

	targetDir := "/target"
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(targetDir)

	mp, err := mount.Mount(os.Getenv("P9_TARGET"), targetDir, "9p", fmt.Sprintf("trans=tcp,msize=4096,port=%s", os.Getenv("P9_PORT")), 0)
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

		xattrs, err := xattr.List(p)
		if err != nil {
			t.Fatalf("Listxattr() = %v", err)
		}

		t.Logf("Xattrs: %v", xattrs)

		want := []string{
			"user.p9.test",
			"user.p9.test2",
		}
		sort.Strings(xattrs)
		sort.Strings(want)
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

		got, err := xattr.Get(p, "user.p9.test")
		if err != nil {
			t.Fatalf("Getxattr() = %v", err)
		}

		if string(got) != "y" {
			t.Errorf("Getxattr = %s, want y", got)
		}
	})

	t.Run("xattr-remove", func(t *testing.T) {
		p := filepath.Join(targetDir, "xattrremove")
		if err := ioutil.WriteFile(p, []byte("somecontent"), 0755); err != nil {
			t.Fatal(err)
		}

		if err := unix.Setxattr(p, "user.p9.test", []byte("y"), 0); err != nil {
			t.Fatalf("Setxattr: %v", err)
		}
		if err := unix.Setxattr(p, "user.p9.test2", []byte("y"), 0); err != nil {
			t.Fatalf("Setxattr: %v", err)
		}

		if err := unix.Removexattr(p, "user.p9.test"); err != nil {
			t.Errorf("Removexattr = %v", err)
		}

		xattrs, err := xattr.List(p)
		if err != nil {
			t.Fatalf("Listxattr() = %v", err)
		}

		want := []string{
			"user.p9.test2",
		}
		if !reflect.DeepEqual(xattrs, want) {
			t.Errorf("Listxattr = %v, want %v", xattrs, want)
		}
	})

	if err := mp.Unmount(0); err != nil {
		t.Fatal(err)
	}
}

func TestGuestServer(t *testing.T) {
	testutil.SkipIfNotRoot(t)

	tmp := t.TempDir()
	mp, err := mount.Mount("", tmp, "tmpfs", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer mp.Unmount(0)

	serverSocket, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("err binding: %v", err)
	}
	serverPort := serverSocket.Addr().(*net.TCPAddr).Port

	// Run the server.
	s := p9.NewServer(localfs.Attacher(tmp), p9.WithServerLogger(ulogtest.Logger{TB: t}))
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		s.Serve(serverSocket)
		wg.Done()
	}()
	defer wg.Wait()
	defer serverSocket.Close()

	targetDir := "/guesttarget"
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(targetDir)

	p9mp, err := mount.Mount("127.0.0.1", targetDir, "9p", fmt.Sprintf("trans=tcp,msize=4096,port=%d", serverPort), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer p9mp.Unmount(0)

	t.Run("xattr-set-larger-than-msize", func(t *testing.T) {
		p := filepath.Join(targetDir, "xattrlargerthanmsize")
		if err := ioutil.WriteFile(p, []byte("somecontent"), 0755); err != nil {
			t.Fatal(err)
		}

		attr := bytes.Repeat([]byte("y"), 5000)
		if err := unix.Setxattr(p, "trusted.p9.test", attr, 0); err != nil {
			t.Fatalf("Setxattr = %v", err)
		}

		got, err := xattr.Get(p, "trusted.p9.test")
		if err != nil {
			t.Fatalf("Getxattr = %v", err)
		}

		if !bytes.Equal(got, attr) {
			t.Errorf("Large getattr = got len %d, want len %d", len(got), len(attr))
		}
	})

	t.Run("xattr-list-large", func(t *testing.T) {
		p := filepath.Join(targetDir, "xattrlistlarge")
		if err := ioutil.WriteFile(p, []byte("somecontent"), 0755); err != nil {
			t.Fatal(err)
		}

		var attrs []string

		const alphabet = "ABCDEFGHIJKLMNOPQRST" // UVWXYZ"
		for _, a := range alphabet {
			// Max attribute name size: 255
			attr := fmt.Sprintf("trusted.p9.%s%c", bytes.Repeat([]byte("z"), 242), a)
			attrs = append(attrs, attr)
			if err := unix.Setxattr(p, attr, []byte("y"), 0); err != nil {
				t.Fatalf("Setxattr(%s) = %v", attr, err)
			}
		}

		xattrs, err := xattr.List(p)
		if err != nil {
			t.Fatalf("Listxattr() = %v", err)
		}

		sort.Strings(xattrs)
		sort.Strings(attrs)
		if !reflect.DeepEqual(xattrs, attrs) {
			t.Errorf("Listxattr = %v, want %v", xattrs, attrs)
		}
	})
}
