// Package rovmtests_test uses the Linux kernel client to mount a 9P file
// system for reading and performs tests on that file system.
package rovmtests_test

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/hugelgupf/p9/fsimpl/test/rovmtests"
	"github.com/hugelgupf/vmtest/guest"
	"github.com/u-root/u-root/pkg/mount"
	"github.com/u-root/u-root/pkg/sh"
	"golang.org/x/exp/slices"
)

func TestMain(m *testing.M) {
	if os.Getuid() == 0 {
		if err := sh.RunWithLogs("dhclient", "-ipv6=false"); err != nil {
			log.Fatalf("could not configure network for tests: %v", err)
		}
	}

	os.Exit(m.Run())
}

func TestMatchContents(t *testing.T) {
	guest.SkipIfNotInVM(t)

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

	wantBytes, err := os.ReadFile("/etc/want.json")
	if err != nil {
		t.Fatalf("Could not find expectations: %v", err)
	}
	var want rovmtests.Expectations
	if err := json.Unmarshal(wantBytes, &want); err != nil {
		t.Fatal(err)
	}

	for _, dir := range want.Dirs {
		t.Run(fmt.Sprintf("dir-%s", dir.Path), func(t *testing.T) {
			dirEntries, err := os.ReadDir(filepath.Join(targetDir, dir.Path))
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, entry := range dirEntries {
				names = append(names, entry.Name())
			}
			slices.Sort(names)
			slices.Sort(dir.Members)
			if !slices.Equal(names, dir.Members) {
				t.Fatalf("Readdir = %v, want %v", names, dir.Members)
			}
		})
	}

	for _, file := range want.Files {
		t.Run(fmt.Sprintf("file-%s", file.Path), func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join(targetDir, file.Path))
			if err != nil {
				t.Fatal(err)
			}

			if got := string(content); got != file.Content {
				t.Fatalf("Readfile = %s, want %s", got, file.Content)
			}
		})
	}

	for _, symlink := range want.Symlinks {
		t.Run(fmt.Sprintf("symlink-%s", symlink.Path), func(t *testing.T) {
			target, err := os.Readlink(filepath.Join(targetDir, symlink.Path))
			if err != nil {
				t.Fatal(err)
			}

			if target != symlink.Target {
				t.Fatalf("Readlink = %s, want %s", target, symlink.Target)
			}
		})
	}
}
