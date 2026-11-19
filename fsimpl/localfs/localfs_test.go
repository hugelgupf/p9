// Copyright 2018 The gVisor Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package localfs

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/hugelgupf/p9/fsimpl/test"
	"github.com/hugelgupf/p9/internal"
	"github.com/hugelgupf/p9/p9"
)

func TestLocalFS(t *testing.T) {
	tempDir, err := ioutil.TempDir("", "localfs-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	test.TestFile(t, Attacher(tempDir))
	test.TestReadOnlyFS(t, Attacher(tempDir))
	test.TestReadWriteFS(t, Attacher(tempDir))
}

func TestSetAttr(t *testing.T) {
	tempDir := t.TempDir()
	name := "file"
	path := filepath.Join(tempDir, name)
	if err := os.WriteFile(path, []byte("hello world"), 0666); err != nil {
		t.Fatal(err)
	}

	// Recorded before the mtime-only update below, which must leave it alone.
	// internal.InfoToStat does not report an access time on Windows.
	checkAtime := runtime.GOOS != "windows"
	atimeBefore := accessTime(t, path)

	root, err := Attacher(tempDir).Attach()
	if err != nil {
		t.Fatal(err)
	}
	_, file, err := root.Walk([]string{name})
	if err != nil {
		t.Fatal(err)
	}

	// Whole seconds: Windows stores 100ns ticks and some file systems round
	// to a second, which would make an exact comparison flaky.
	mtime := time.Unix(1234, 0)
	if err := file.SetAttr(
		p9.SetAttrMask{Permissions: true, Size: true, MTime: true, MTimeNotSystemTime: true},
		p9.SetAttr{
			Permissions:      p9.Setuid | 0600,
			Size:             5,
			MTimeSeconds:     uint64(mtime.Unix()),
			MTimeNanoSeconds: uint64(mtime.Nanosecond()),
		},
	); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows chmod only implements the write bit.
	if runtime.GOOS != "windows" {
		if got, want := info.Mode(), os.ModeSetuid|os.FileMode(0600); got != want {
			t.Errorf("mode = %v, want %v", got, want)
		}
	}
	if got := info.Size(); got != 5 {
		t.Errorf("size = %d, want 5", got)
	}
	if got := info.ModTime(); !got.Equal(mtime) {
		t.Errorf("mtime = %v, want %v", got, mtime)
	}
	if checkAtime {
		if got := accessTime(t, path); !got.Equal(atimeBefore) {
			t.Errorf("atime after setting mtime = %v, want %v", got, atimeBefore)
		}
	}

	// Setting the access time must leave the modification time alone.
	atime := time.Unix(2345, 0)
	if err := file.SetAttr(
		p9.SetAttrMask{ATime: true, ATimeNotSystemTime: true},
		p9.SetAttr{
			ATimeSeconds:     uint64(atime.Unix()),
			ATimeNanoSeconds: uint64(atime.Nanosecond()),
		},
	); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if checkAtime {
		if got := accessTime(t, path); !got.Equal(atime) {
			t.Errorf("atime = %v, want %v", got, atime)
		}
	}
	if got := info.ModTime(); !got.Equal(mtime) {
		t.Errorf("mtime after setting atime = %v, want %v", got, mtime)
	}

	if runtime.GOOS == "windows" {
		return
	}
	_, _, ga, err := file.GetAttr(p9.AttrMaskAll)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.SetAttr(p9.SetAttrMask{UID: true, GID: true}, p9.SetAttr{UID: ga.UID, GID: ga.GID}); err != nil {
		t.Fatalf("SetAttr(UID, GID) = %v, want nil", err)
	}

	// Truncating and setting permissions to 0 in a single SetAttr
	// call must succeed (truncate runs before chmod 0).
	if err := file.SetAttr(p9.SetAttrMask{Size: true, Permissions: true}, p9.SetAttr{Size: 2, Permissions: 0}); err != nil {
		t.Fatalf("SetAttr(Size=2, Permissions=0) = %v, want nil", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0600) })
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Size(); got != 2 {
		t.Errorf("size = %d, want 2", got)
	}
	if got := info.Mode().Perm(); got != 0 {
		t.Errorf("perm = %#o, want 0", got)
	}
}

func TestSetAttrSymlink(t *testing.T) {
	// The target lives outside the exported directory.
	target := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(target, []byte("hello world"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}

	root, err := Attacher(t.TempDir()).Attach()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := root.Symlink(target, "link", p9.NoUID, p9.NoGID); err != nil {
		t.Skipf("Symlink() = %v", err)
	}
	_, file, err := root.Walk([]string{"link"})
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		valid p9.SetAttrMask
		attr  p9.SetAttr
	}{
		{p9.SetAttrMask{Size: true}, p9.SetAttr{Size: 0}},
		{p9.SetAttrMask{Permissions: true}, p9.SetAttr{Permissions: 0777}},
		{p9.SetAttrMask{MTime: true, MTimeNotSystemTime: true}, p9.SetAttr{MTimeSeconds: 1234}},
	} {
		if err := file.SetAttr(tt.valid, tt.attr); err == nil {
			t.Errorf("SetAttr(%v) on symlink = nil, want error", tt.valid)
		}
	}

	after, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() != before.Size() || after.Mode() != before.Mode() || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("symlink target changed: size %d, mode %v, mtime %v; want %d, %v, %v",
			after.Size(), after.Mode(), after.ModTime(), before.Size(), before.Mode(), before.ModTime())
	}

	if runtime.GOOS == "windows" {
		return
	}
	// Ownership of the link itself can still be set.
	_, _, ga, err := file.GetAttr(p9.AttrMaskAll)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.SetAttr(p9.SetAttrMask{UID: true, GID: true}, p9.SetAttr{UID: ga.UID, GID: ga.GID}); err != nil {
		t.Fatalf("SetAttr(UID, GID) on symlink = %v, want nil", err)
	}
}

func TestWriteAtAppend(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "append.txt")
	if err := os.WriteFile(path, []byte("hello"), 0666); err != nil {
		t.Fatal(err)
	}

	root, err := Attacher(tempDir).Attach()
	if err != nil {
		t.Fatal(err)
	}
	_, file, err := root.Walk([]string{"append.txt"})
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := file.Open(p9.WriteOnly | p9.OpenFlags(os.O_APPEND)); err != nil {
		file.Close()
		t.Fatalf("Open(O_APPEND) = %v", err)
	}
	if _, err := file.WriteAt([]byte(" world"), 0); err != nil {
		file.Close()
		t.Fatalf("WriteAt on O_APPEND file = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello world" {
		t.Errorf("got %q, want %q", string(got), "hello world")
	}
	if err := file.(*Local).Remove(); err != nil {
		t.Fatalf("Remove() = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("Stat after Remove() = %v, want not-exist", err)
	}
}

func TestReaddirPagination(t *testing.T) {
	tempDir := t.TempDir()
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		if err := os.WriteFile(filepath.Join(tempDir, name), []byte(name), 0666); err != nil {
			t.Fatal(err)
		}
	}

	root, err := Attacher(tempDir).Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	if _, _, err := root.Open(p9.ReadOnly); err != nil {
		t.Fatal(err)
	}

	var got []string
	var offset uint64
	for {
		ents, err := root.Readdir(offset, 2)
		if err != nil {
			t.Fatalf("Readdir(offset=%d) = %v", offset, err)
		}
		if len(ents) == 0 {
			break
		}
		for _, e := range ents {
			got = append(got, e.Name)
		}
		offset = ents[len(ents)-1].Offset
	}
	if want := []string{"a", "b", "c", "d", "e"}; !slices.Equal(got, want) {
		t.Fatalf("paginated Readdir got %v, want %v", got, want)
	}

	// Out-of-range offset (including high bit set) must return empty
	// without panicking.
	for _, off := range []uint64{5, 100, 1 << 63} {
		ents, err := root.Readdir(off, 2)
		if err != nil {
			t.Fatalf("Readdir(offset=%d) = %v", off, err)
		}
		if len(ents) != 0 {
			t.Fatalf("Readdir(offset=%d) = %v, want empty", off, ents)
		}
	}
}

// accessTime returns the file's access time.
func accessTime(t *testing.T, path string) time.Time {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat := internal.InfoToStat(info)
	return time.Unix(stat.Atim.Sec, stat.Atim.Nsec)
}
