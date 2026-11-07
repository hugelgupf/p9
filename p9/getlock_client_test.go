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

package p9_test

import (
	"net"
	"testing"

	"github.com/hugelgupf/p9/fsimpl/templatefs"
	"github.com/hugelgupf/p9/p9"
)

var lockQID = p9.QID{Type: p9.TypeRegular, Path: 1}

// lockFile is a single-node file that does not implement LockGetter.
type lockFile struct {
	templatefs.NoopFile
}

func (f *lockFile) Walk([]string) ([]p9.QID, p9.File, error) {
	return []p9.QID{lockQID}, f, nil
}

func (f *lockFile) GetAttr(p9.AttrMask) (p9.QID, p9.AttrMask, p9.Attr, error) {
	return lockQID, p9.AttrMask{Mode: true}, p9.Attr{Mode: p9.ModeRegular | 0o644}, nil
}

// heldLockFile implements LockGetter, reporting held as the conflicting lock
// and recording the request it was asked about.
type heldLockFile struct {
	lockFile
	held p9.LockType
	got  []any
}

func (f *heldLockFile) Walk([]string) ([]p9.QID, p9.File, error) {
	return []p9.QID{lockQID}, f, nil
}

func (f *heldLockFile) GetLock(pid int, locktype p9.LockType, start, length uint64, client string) (p9.LockType, uint64, uint64, int, string, error) {
	f.got = []any{pid, locktype, start, length, client}
	return f.held, 10, 20, 42, "holder", nil
}

type fileAttacher struct{ root p9.File }

func (a fileAttacher) Attach() (p9.File, error) { return a.root, nil }

func serveFile(t *testing.T, root p9.File) p9.File {
	t.Helper()
	srv, cli := net.Pipe()
	done := make(chan struct{})
	go func() {
		_ = p9.NewServer(fileAttacher{root}).Handle(srv, srv)
		close(done)
	}()
	t.Cleanup(func() {
		_ = cli.Close()
		_ = srv.Close()
		<-done
	})
	c, err := p9.NewClient(cli)
	if err != nil {
		t.Fatalf("NewClient: got = %v, want = nil", err)
	}
	f, err := c.Attach("")
	if err != nil {
		t.Fatalf("Attach: got = %v, want = nil", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestClientGetLock(t *testing.T) {
	t.Run("Conflict", func(t *testing.T) {
		root := &heldLockFile{held: p9.WriteLock}
		f := serveFile(t, root)
		typ, start, length, pid, client, err := f.(p9.LockGetter).GetLock(7, p9.ReadLock, 100, 5, "vm")
		if err != nil {
			t.Fatalf("GetLock: got = %v, want = nil", err)
		}
		if typ != p9.WriteLock || start != 10 || length != 20 || pid != 42 || client != "holder" {
			t.Errorf("GetLock: got = %v %d %d %d %q, want = WriteLock 10 20 42 \"holder\"", typ, start, length, pid, client)
		}
		want := []any{7, p9.ReadLock, uint64(100), uint64(5), "vm"}
		for i := range want {
			if root.got[i] != want[i] {
				t.Errorf("server saw %v, want %v", root.got, want)
				break
			}
		}
	})

	t.Run("Unimplemented", func(t *testing.T) {
		f := serveFile(t, &lockFile{})
		typ, start, length, pid, _, err := f.(p9.LockGetter).GetLock(7, p9.WriteLock, 100, 5, "vm")
		if err != nil {
			t.Fatalf("GetLock: got = %v, want = nil", err)
		}
		if typ != p9.Unlock || start != 100 || length != 5 || pid != 7 {
			t.Errorf("GetLock: got = %v %d %d %d, want = Unlock 100 5 7", typ, start, length, pid)
		}
	})
}
