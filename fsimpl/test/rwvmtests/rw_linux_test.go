// Package rwvmtests_test uses the Linux kernel client to mount a 9P file
// system for reading and writing and performs tests on that file system.
package rwvmtests_test

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/u-root/u-root/pkg/mount"
	"github.com/u-root/u-root/pkg/sh"
	"github.com/u-root/u-root/pkg/testutil"
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

	want := "haha"
	if err := ioutil.WriteFile(filepath.Join(targetDir, "foobar"), []byte(want), 0755); err != nil {
		t.Error(err)
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

	if err := mp.Unmount(0); err != nil {
		t.Fatal(err)
	}
}
