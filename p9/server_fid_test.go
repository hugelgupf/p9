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

package p9_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hugelgupf/p9/fsimpl/templatefs"
	"github.com/hugelgupf/p9/linux"
	"github.com/hugelgupf/p9/p9"
)

// memNode is an inode in a memFS.
type memNode struct {
	qid      p9.QID
	mode     p9.FileMode
	children map[string]*memNode
}

// memFS is a minimal in-memory file system that records which calls the
// server lets through to the backend.
type memFS struct {
	mu       sync.Mutex
	nextPath uint64
	root     *memNode

	// openHook, if set, is called by every Open, and its error is
	// returned.
	openHook func() error

	// opens counts calls to Open.
	opens atomic.Int32
}

func newMemFS() *memFS {
	fs := &memFS{}
	fs.root = fs.newNode(p9.ModeDirectory)
	return fs
}

func (fs *memFS) newNode(mode p9.FileMode) *memNode {
	fs.nextPath++
	n := &memNode{
		qid:  p9.QID{Type: mode.QIDType(), Path: fs.nextPath},
		mode: mode,
	}
	if mode.IsDir() {
		n.children = make(map[string]*memNode)
	}
	return n
}

// add creates a node named name in dir.
func (fs *memFS) add(dir *memNode, name string, mode p9.FileMode) *memNode {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	n := fs.newNode(mode)
	dir.children[name] = n
	return n
}

// memFile is a p9.File backed by a memNode.
type memFile struct {
	templatefs.NoopFile

	fs   *memFS
	node *memNode
}

func (f *memFile) Walk(names []string) ([]p9.QID, p9.File, error) {
	if len(names) == 0 {
		return nil, &memFile{fs: f.fs, node: f.node}, nil
	}
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	n, ok := f.node.children[names[0]]
	if !ok {
		return nil, nil, linux.ENOENT
	}
	return []p9.QID{n.qid}, &memFile{fs: f.fs, node: n}, nil
}

func (f *memFile) GetAttr(p9.AttrMask) (p9.QID, p9.AttrMask, p9.Attr, error) {
	return f.node.qid, p9.AttrMask{Mode: true}, p9.Attr{Mode: f.node.mode}, nil
}

func (f *memFile) Open(p9.OpenFlags) (p9.QID, uint32, error) {
	f.fs.opens.Add(1)
	if f.fs.openHook != nil {
		if err := f.fs.openHook(); err != nil {
			return p9.QID{}, 0, err
		}
	}
	return f.node.qid, 0, nil
}

// walk walks from f, failing the test on error. The new file is closed when
// the test ends, rather than by a finalizer in the middle of it.
func walk(t *testing.T, f p9.File, names ...string) p9.File {
	t.Helper()
	_, nf, err := f.Walk(names)
	if err != nil {
		t.Fatalf("Walk(%q): got = %v, want = nil", names, err)
	}
	t.Cleanup(func() { _ = nf.Close() })
	return nf
}

// TestConcurrentTlopenOpensOnce checks that concurrent Tlopens of one fid call
// File.Open once, and that reads racing with them see a consistent open state.
func TestConcurrentTlopenOpensOnce(t *testing.T) {
	const n = 8

	fs := newMemFS()
	fs.add(fs.root, "f", p9.ModeRegular|0o644)
	entered := make(chan struct{}, n)
	release := make(chan struct{})
	fs.openHook = func() error {
		entered <- struct{}{}
		<-release
		return nil
	}
	f := walk(t, serveFile(t, &memFile{fs: fs, node: fs.root}), "f")

	var wg sync.WaitGroup
	errs := make(chan error, n)
	for range n {
		wg.Go(func() {
			_, _, err := f.Open(p9.ReadOnly)
			errs <- err
		})
	}
	// Tread reads the open state that Tlopen sets.
	stop := make(chan struct{})
	wg.Go(func() {
		buf := make([]byte, 1)
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = f.ReadAt(buf, 0)
			}
		}
	})

	<-entered
	// Give the other Tlopens time to reach the server.
	time.Sleep(100 * time.Millisecond)
	close(release)
	close(stop)
	wg.Wait()
	close(errs)

	var succeeded int
	for err := range errs {
		switch {
		case err == nil:
			succeeded++
		case !errors.Is(err, linux.EINVAL):
			t.Errorf("Open: got = %v, want = nil or EINVAL", err)
		}
	}
	if succeeded != 1 {
		t.Errorf("successful Opens: got = %d, want = 1", succeeded)
	}
	if got := fs.opens.Load(); got != 1 {
		t.Errorf("File.Open calls: got = %d, want = 1", got)
	}
}

// TestTlopenRetryAfterFailedOpen checks that a failed File.Open leaves the fid
// unopened, so that it may be opened again.
func TestTlopenRetryAfterFailedOpen(t *testing.T) {
	fs := newMemFS()
	fs.add(fs.root, "f", p9.ModeRegular|0o644)
	fs.openHook = func() error {
		if fs.opens.Load() == 1 {
			return linux.EACCES
		}
		return nil
	}
	f := walk(t, serveFile(t, &memFile{fs: fs, node: fs.root}), "f")

	if _, _, err := f.Open(p9.ReadOnly); !errors.Is(err, linux.EACCES) {
		t.Fatalf("first Open: got = %v, want = EACCES", err)
	}
	if _, _, err := f.Open(p9.ReadOnly); err != nil {
		t.Fatalf("second Open: got = %v, want = nil", err)
	}
	if _, _, err := f.Open(p9.ReadOnly); !errors.Is(err, linux.EINVAL) {
		t.Fatalf("third Open: got = %v, want = EINVAL", err)
	}
}
