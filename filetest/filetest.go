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

// Package filetest implements p9.File acceptance tests.
package filetest

import (
	"fmt"
	"testing"

	"github.com/hugelgupf/p9/p9"
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

// TestReadOnlyFS tests attach for all expected p9.Attacher and p9.File
// behaviors on read-only file systems.
func TestReadOnlyFS(t *testing.T, attach p9.Attacher) {
	root, err := attach.Attach()
	if err != nil {
		t.Fatalf("Failed to attach to %v: %v", attach, err)
	}

	t.Run("walk-self", func(t *testing.T) { testWalkSelf(t, root) })
	t.Run("readdir-walk", func(t *testing.T) { testReaddirWalk(t, root) })
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
	for _, names := range [][]string{nil, []string{}} {
		t.Run(fmt.Sprintf("self-%#v", names), func(t *testing.T) {
			qids, got, err := root.Walk(names)
			if err != nil {
				t.Errorf("Walk(%v, %#v) = %v, want %v", root, names, err, nil)
			}
			if len(qids) != 1 {
				t.Errorf("Walk(%v, %#v) = %d QIDs, want 1", root, names, len(qids))
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
