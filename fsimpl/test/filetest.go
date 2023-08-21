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

// Package test implements p9.File acceptance tests.
package test

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/hugelgupf/p9/p9"
	"github.com/u-root/uio/uio"
	"golang.org/x/exp/slices"
)

// TestFile tests attach for all expected p9.Attacher and p9.File behaviors.
//
// TestFile expects attach to attach to an empty, writable directory.
func TestFile(t *testing.T, attach p9.Attacher) {
	root, err := attach.Attach()
	if err != nil {
		t.Fatalf("Failed to attach to %v: %v", attach, err)
	}

	t.Run("walk-self", func(t *testing.T) { testWalkSelf(t, root) })
	t.Run("create-readdir", func(t *testing.T) { testCreate(t, root) })
	t.Run("readdir-walk", func(t *testing.T) { testReaddirWalk(t, root) })
}

type file struct {
	content string
	attr    p9.Attr
	mask    p9.AttrMask
}

type dir struct {
	members []string
}

type expect struct {
	files map[string]file
	dirs  map[string]dir
}

type Expect func(e *expect)

func WithDir(path string, members ...string) Expect {
	return func(e *expect) {
		e.dirs[path] = dir{
			members: members,
		}
	}
}

func WithFile(path string, content string, attr p9.Attr, mask p9.AttrMask) Expect {
	return func(e *expect) {
		e.files[path] = file{
			content: content,
			attr:    attr,
			mask:    mask,
		}
	}
}

func WithSymlink(path string, target string, attr p9.Attr, mask p9.AttrMask) Expect {
	return func(e *expect) {
		e.files[path] = file{
			content: target,
			attr:    attr,
			mask:    mask,
		}
	}
}

// TestReadOnlyFS tests attach for all expected p9.Attacher and p9.File
// behaviors on read-only file systems.
func TestReadOnlyFS(t *testing.T, attach p9.Attacher, expectations ...Expect) {
	root, err := attach.Attach()
	if err != nil {
		t.Fatalf("Failed to attach to %v: %v", attach, err)
	}

	t.Run("walk-self", func(t *testing.T) { testWalkSelf(t, root) })
	t.Run("readdir-walk", func(t *testing.T) { testReaddirWalk(t, root) })

	e := expect{
		files: make(map[string]file),
		dirs:  make(map[string]dir),
	}
	for _, exp := range expectations {
		exp(&e)
	}
	for path, dir := range e.dirs {
		t.Run(fmt.Sprintf("dir-%s", path), func(t *testing.T) { testDirContents(t, root, path, dir) })
	}
	for path, file := range e.files {
		t.Run(fmt.Sprintf("file-%s", path), func(t *testing.T) { testIsFile(t, root, path, file) })
	}
}

func testDirContents(t *testing.T, root p9.File, path string, d dir) {
	var dest []string
	if len(path) > 0 {
		dest = strings.Split(path, "/")
	}
	_, f, err := root.Walk(dest)
	if err != nil {
		t.Fatalf("Walk(%s) failed: %s", path, err)
	}

	_, _, attr, err := f.GetAttr(p9.AttrMask{Mode: true})
	if err != nil {
		t.Fatalf("GetAttr = %v", err)
	}
	if !attr.Mode.IsDir() {
		t.Fatalf("GetAttr mode = %v, wanted directory", attr.Mode)
	}

	if _, _, err := f.Open(p9.ReadOnly); err != nil {
		t.Fatalf("Open = %v", err)
	}

	var dirents []p9.Dirent
	offset := uint64(0)
	for {
		d, err := f.Readdir(offset, 1000)
		if len(d) == 0 || err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Readdir: %v", err)
		}
		dirents = append(dirents, d...)
		offset = d[len(d)-1].Offset
	}

	var names []string
	for _, entry := range dirents {
		names = append(names, entry.Name)
	}

	slices.Sort(d.members)
	slices.Sort(names)
	if !slices.Equal(names, d.members) {
		t.Fatalf("Readdir = %v, wanted %v", names, d.members)
	}

	for _, entry := range dirents {
		qids, _, err := f.Walk([]string{entry.Name})
		if err != nil {
			t.Fatalf("Could not walk to %s: %v", entry.Name, err)
		}
		if qids[0] != entry.QID {
			t.Fatalf("For %s: Readdir QID is %v, Walk QID is %v, expected same", entry.Name, entry.QID, qids[0])
		}
	}

	if err := f.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}
}

func testIsFile(t *testing.T, root p9.File, path string, file file) {
	_, f, err := root.Walk(strings.Split(path, "/"))
	if err != nil {
		t.Fatalf("Walk(%s) failed: %s", path, err)
	}

	_, _, attr, err := f.GetAttr(file.mask)
	if err != nil {
		t.Fatalf("GetAttr = %v", err)
	}
	if got := attr.WithMask(file.mask); got != file.attr {
		t.Fatalf("GetAttr = %v, want %v", got, file.attr)
	}

	switch {
	case file.attr.Mode.IsRegular():
		if _, _, err := f.Open(p9.ReadOnly); err != nil {
			t.Fatalf("Open = %v, want nil", err)
		}

		for i := 0; i < 2; i++ {
			con, err := uio.ReadAll(f)
			if err != nil {
				t.Fatalf("ReadAll(%d) = %v, want nil", i, err)
			}
			if got := string(con); got != file.content {
				t.Fatalf("ReadAll(%d) = %v, want %v", i, got, file.content)
			}
		}

		if err := f.Close(); err != nil {
			t.Errorf("Close = %v, want nil", err)
		}

	case file.attr.Mode.IsSymlink():
		target, err := f.Readlink()
		if err != nil {
			t.Fatalf("Readlink = %v", err)
		}
		if target != file.content {
			t.Fatalf("Readlink = %s, want %s", target, file.content)
		}
	}
}

func testCreate(t *testing.T, root p9.File) {
	_, f1, err := root.Walk(nil)
	if err != nil {
		t.Fatal(err)
	}

	_, _, _, err = f1.Create("file2", p9.ReadWrite, 0777, p9.NoUID, p9.NoGID)
	if err != nil {
		t.Errorf("Create(file2) = %v, want nil", err)
	}

	_, _, _, err = f1.Create("file2", p9.ReadWrite, 0777, p9.NoUID, p9.NoGID)
	if err == nil {
		t.Errorf("Create(file2) (2nd time) = %v, want EEXIST", err)
	}

	dirents, err := readdir(root)
	if err != nil {
		t.Fatal(err)
	}
	file2dirent := dirents.Find("file2")
	if file2dirent == nil {
		t.Errorf("Dirents(%v).Find(file2) = nil, but should be there", dirents)
	}
}

func readdir(dir p9.File) (p9.Dirents, error) {
	_, dirCopy, err := dir.Walk(nil)
	if err != nil {
		return nil, fmt.Errorf("dir(%v).Walk() = %v, want nil (dir should be able to walk to itself)", dir, err)
	}
	_, _, err = dirCopy.Open(p9.ReadOnly)
	if err != nil {
		return nil, fmt.Errorf("dir(%v).Open(ReadOnly) = %v, want nil (directory must be open-able to readdir)", dir, err)
	}
	dirents, err := dirCopy.Readdir(0, 10)
	if err != nil {
		return nil, fmt.Errorf("Readdir(dir) = %v, want nil", err)
	}
	return dirents, nil
}

func testWalkSelf(t *testing.T, root p9.File) {
	for _, names := range [][]string{nil, {}} {
		t.Run(fmt.Sprintf("self-%#v", names), func(t *testing.T) {
			_, got, err := root.Walk(names)
			if err != nil {
				t.Errorf("Walk(%v, %#v) = %v, want %v", root, names, err, nil)
			}
			testSameFile(t, root, got)
		})
	}
}

func testReaddirWalk(t *testing.T, root p9.File) {
	dirents, err := readdir(root)
	if err != nil {
		t.Fatal(err)
	}

	for _, dirent := range dirents {
		t.Run(fmt.Sprintf("walk-to-%s", dirent.Name), func(t *testing.T) {
			qids, got, err := root.Walk([]string{dirent.Name})
			if err != nil {
				t.Errorf("Walk(%v, %s) = %v, want %v", root, dirent.Name, err, nil)
			}
			if len(qids) != 1 {
				t.Errorf("Walk(%v, %s) = %d QIDs, want 1", root, dirent.Name, len(qids))
			}
			qid, _, _, err := got.GetAttr(p9.AttrMaskAll)
			if err != nil {
				t.Errorf("GetAttr(%v) = %v, want nil", got, err)
			}
			if qids[0] != qid {
				t.Errorf("GetAttr(%v) = %v, want %v (same QID as returned by Walk)", got, qid, qids[0])
			}
		})
	}
}

func testSameFile(t *testing.T, f1, f2 p9.File) {
	f1QID, _, f1Attr, err := f1.GetAttr(p9.AttrMaskAll)
	if err != nil {
		t.Errorf("Attr(%v) = %v, want nil", f1, err)
	}
	f2QID, _, f2Attr, err := f2.GetAttr(p9.AttrMaskAll)
	if err != nil {
		t.Errorf("Attr(%v) = %v, want nil", f2, err)
	}

	if f1QID != f2QID {
		t.Errorf("QIDs of same files do not match: %v and %v", f1QID, f2QID)
	}
	if f1Attr != f2Attr {
		t.Errorf("Attributes of same files do not match: %v and %v", f1Attr, f2Attr)
	}
}
