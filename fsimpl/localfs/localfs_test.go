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
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
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

	// Each 1-byte name encodes to 24 + 1 = 25 bytes, so 50 bytes fits at
	// most 2 entries per call and 24 bytes fits 0 entries.
	if ents, err := root.Readdir(0, 24); err != nil || len(ents) != 0 {
		t.Fatalf("Readdir(0, 24) = (%v, %v), want empty, nil", ents, err)
	}

	var got []string
	var offset uint64
	for {
		ents, err := root.Readdir(offset, 50)
		if err != nil {
			t.Fatalf("Readdir(offset=%d) = %v", offset, err)
		}
		if len(ents) == 0 {
			break
		}
		if len(ents) > 2 {
			t.Fatalf("Readdir(offset=%d, count=50) returned %d entries (%d bytes), want <= 2", offset, len(ents), len(ents)*25)
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
		ents, err := root.Readdir(off, 8192)
		if err != nil {
			t.Fatalf("Readdir(offset=%d) = %v", off, err)
		}
		if len(ents) != 0 {
			t.Fatalf("Readdir(offset=%d) = %v, want empty", off, ents)
		}
	}
}

// 9P2000.L Rreaddir wire d_type constants (POSIX/Linux DT_*).
const (
	dtFIFO = p9.QIDType(1)
	dtDir  = p9.QIDType(4)
	dtReg  = p9.QIDType(8)
	dtLnk  = p9.QIDType(10)
	dtSock = p9.QIDType(12)
)

func serveLocalFS(t *testing.T, dir string) p9.File {
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

	attached, err := c.Attach("")
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	t.Cleanup(func() { _ = attached.Close() })
	return attached
}

func TestReaddirDirentTypes(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "reg"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(tempDir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	wantTypes := map[string]p9.QIDType{
		"reg": dtReg,
		"sub": dtDir,
	}
	wantQIDTypes := map[string]p9.QIDType{
		"reg": p9.TypeRegular,
		"sub": p9.TypeDir,
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink("reg", filepath.Join(tempDir, "sym")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("nonexistent", filepath.Join(tempDir, "broken")); err != nil {
			t.Fatal(err)
		}
		wantTypes["sym"] = dtLnk
		wantTypes["broken"] = dtLnk
		wantQIDTypes["sym"] = p9.TypeSymlink
		wantQIDTypes["broken"] = p9.TypeSymlink
	}

	root := serveLocalFS(t, tempDir)

	if _, _, err := root.Open(p9.ReadOnly); err != nil {
		t.Fatal(err)
	}
	ents, err := root.Readdir(0, 8192)
	if err != nil {
		t.Fatalf("Readdir = %v", err)
	}
	if len(ents) != len(wantTypes) {
		t.Fatalf("got %d dirents (%v), want %d", len(ents), ents, len(wantTypes))
	}
	for _, e := range ents {
		if got, want := e.Type, wantTypes[e.Name]; got != want {
			t.Errorf("entry %q: Dirent.Type = %#x, want %#x", e.Name, uint8(got), uint8(want))
		}
		if got, want := e.QID.Type, wantQIDTypes[e.Name]; got != want {
			t.Errorf("entry %q: QID.Type = %#x, want %#x", e.Name, uint8(got), uint8(want))
		}
	}
}

func TestReaddirSnapshotUnlinkAndRewind(t *testing.T) {
	tempDir := t.TempDir()
	for i := range 20 {
		name := fmt.Sprintf("file-%02d", i)
		if err := os.WriteFile(filepath.Join(tempDir, name), []byte("x"), 0644); err != nil {
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

	// Each "file-XX" entry is 24 + 7 = 31 bytes; budget 62 fits 2 entries.
	page1, err := root.Readdir(0, 62)
	if err != nil {
		t.Fatalf("page1 Readdir = %v", err)
	}
	if len(page1) != 2 || page1[0].Name != "file-00" || page1[1].Name != "file-01" {
		t.Fatalf("page1 = %v, want [file-00 file-01]", page1)
	}

	// Unlink an already-read entry ("file-00") so indices would shift if
	// Readdir re-read the directory, and unlink an upcoming entry
	// ("file-03") to exercise concurrent ENOENT handling.
	if err := os.Remove(filepath.Join(tempDir, "file-00")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(tempDir, "file-03")); err != nil {
		t.Fatal(err)
	}

	got := []string{page1[0].Name, page1[1].Name}
	offset := page1[len(page1)-1].Offset
	for {
		ents, err := root.Readdir(offset, 62)
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

	var want []string
	for i := range 20 {
		if i == 3 {
			continue
		}
		want = append(want, fmt.Sprintf("file-%02d", i))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("snapshot scan got %v, want %v", got, want)
	}

	// Rewinding to offset 0 on the same open handle refreshes the snapshot.
	if err := os.WriteFile(filepath.Join(tempDir, "file-20"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	rewound, err := root.Readdir(0, 8192)
	if err != nil {
		t.Fatalf("rewind Readdir = %v", err)
	}
	var rewoundNames []string
	for _, e := range rewound {
		rewoundNames = append(rewoundNames, e.Name)
	}
	if slices.Contains(rewoundNames, "file-00") || slices.Contains(rewoundNames, "file-03") || !slices.Contains(rewoundNames, "file-20") {
		t.Fatalf("rewound names = %v, want file-20 present and file-00/file-03 absent", rewoundNames)
	}

	// Concurrent Readdir calls on the same open directory must not race.
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := root.Readdir(0, 124); err != nil {
				t.Errorf("concurrent Readdir(0): %v", err)
			}
			if _, err := root.Readdir(2, 124); err != nil {
				t.Errorf("concurrent Readdir(2): %v", err)
			}
		}()
	}
	wg.Wait()
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
