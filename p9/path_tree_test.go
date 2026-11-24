// Copyright 2026 The gVisor Authors.
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

package p9

import (
	"testing"
	"time"
)

// stubFile is a File that can be cloned and renamed. Its other methods
// panic.
type stubFile struct{ File }

func (stubFile) Walk([]string) ([]QID, File, error) { return nil, stubFile{}, nil }
func (stubFile) Renamed(File, string)               {}

// newChildRef returns a fidRef for name in parent, as doWalk would.
func newChildRef(parent *fidRef, name string, mode FileMode) *fidRef {
	ref := &fidRef{
		server:   parent.server,
		parent:   parent,
		file:     stubFile{},
		refs:     1,
		mode:     mode,
		pathNode: parent.pathNode.pathNodeFor(name),
	}
	parent.pathNode.addChild(ref, name)
	parent.IncRef()
	return ref
}

// TestCloneReadsParentUnderRenameMu checks that cloning a fid reads its parent
// only once renameMu is held, as a concurrent rename may change it, and then
// locks the parent it read.
func TestCloneReadsParentUnderRenameMu(t *testing.T) {
	s := NewServer(nil)
	cs := &connState{server: s}
	root := &fidRef{
		server:   s,
		file:     stubFile{},
		refs:     1,
		mode:     ModeDirectory,
		pathNode: s.pathTree,
	}
	d1 := newChildRef(root, "d1", ModeDirectory)
	d2 := newChildRef(root, "d2", ModeDirectory)
	f := newChildRef(d1, "f", ModeRegular)

	// Hold renameMu so that the clone blocks, and rename f from d1 to d2
	// meanwhile. Then hold d2's opMu: the clone must wait for it.
	s.renameMu.Lock()
	done := make(chan *fidRef)
	go func() {
		_, clone, _, _, err := doWalk(cs, f, nil, false)
		if err != nil {
			t.Errorf("doWalk: got = %v, want = nil", err)
		}
		done <- clone
	}()
	time.Sleep(10 * time.Millisecond)
	d1.renameChildTo("f", d2, "f")
	d2.pathNode.opMu.Lock()
	s.renameMu.Unlock()

	select {
	case <-done:
		t.Fatal("clone did not wait for the new parent's opMu")
	case <-time.After(10 * time.Millisecond):
	}
	d2.pathNode.opMu.Unlock()

	clone := <-done
	if clone == nil {
		return
	}
	if clone.parent != d2 {
		t.Errorf("clone parent: got = %p, want = %p (d2)", clone.parent, d2)
	}
	if got := d2.pathNode.nameFor(clone); got != "f" {
		t.Errorf("clone name in d2: got = %q, want = %q", got, "f")
	}
}
