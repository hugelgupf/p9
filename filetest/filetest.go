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
	"testing"

	"github.com/hugelgupf/p9/p9"
)

// TestFile tests attach for all expected p9.Attacher and p9.File behaviors.
func TestFile(t *testing.T, attach p9.Attacher) {
	root, err := attach.Attach()
	if err != nil {
		t.Fatalf("Failed to attach to %v: %v", attach, err)
	}

	t.Run("walk", func(t *testing.T) { testWalk(t, root) })
}

func testWalk(t *testing.T, f p9.File) {
	qids, got, err := f.Walk(nil)
	if err != nil {
		t.Errorf("Walk to self of %v = %v, want %v", f, err, nil)
	}
	if len(qids) != 1 {
		t.Errorf("Walk to self of %v = %d QIDs, want 1", f, len(qids))
	}
	testSameFile(t, f, got)
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
