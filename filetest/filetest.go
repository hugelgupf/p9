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

	t.Run("create", func(t *testing.T) { testCreate(t, root) })
	t.Run("walk", func(t *testing.T) { testWalk(t, root) })
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
}

func testWalk(t *testing.T, root p9.File) {
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
