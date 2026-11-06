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
	"net"
	"reflect"
	"testing"

	"github.com/hugelgupf/p9/fsimpl/templatefs"
	"github.com/hugelgupf/p9/p9"
)

var lockedQID = p9.QID{Type: p9.TypeRegular, Path: 1}

// lockedFile records the Tlock it is asked for. A walk from it reaches
// child.
type lockedFile struct {
	templatefs.NoopFile

	child *lockedFile
	got   []any
}

func (f *lockedFile) Walk([]string) ([]p9.QID, p9.File, error) {
	return []p9.QID{lockedQID}, f.child, nil
}

func (f *lockedFile) GetAttr(p9.AttrMask) (p9.QID, p9.AttrMask, p9.Attr, error) {
	return lockedQID, p9.AttrMask{Mode: true}, p9.Attr{Mode: p9.ModeRegular | 0o644}, nil
}

func (f *lockedFile) Lock(pid int, locktype p9.LockType, flags p9.LockFlags, start, length uint64, client string) (p9.LockStatus, error) {
	f.got = []any{pid, locktype, flags, start, length, client}
	return p9.LockStatusOK, nil
}

type lockedAttacher struct{ f *lockedFile }

func (a lockedAttacher) Attach() (p9.File, error) { return a.f, nil }

func TestClientLockReachesTheFileOfItsFid(t *testing.T) {
	child := &lockedFile{}
	root := &lockedFile{child: child}

	srv, cli := net.Pipe()
	done := make(chan struct{})
	go func() {
		_ = p9.NewServer(lockedAttacher{root}).Handle(srv, srv)
		close(done)
	}()
	defer func() {
		_ = cli.Close()
		<-done
	}()

	c, err := p9.NewClient(cli)
	if err != nil {
		t.Fatalf("NewClient: got = %v, want = nil", err)
	}

	// A walk gives the file a fid other than the root's, which a Tlock
	// without its fid would miss.
	attached, err := c.Attach("")
	if err != nil {
		t.Fatalf("Attach: got = %v, want = nil", err)
	}
	_, f, err := attached.Walk(nil)
	if err != nil {
		t.Fatalf("Walk: got = %v, want = nil", err)
	}

	status, err := f.Lock(7, p9.WriteLock, p9.LockFlagsBlock, 10, 20, "vm")
	if err != nil || status != p9.LockStatusOK {
		t.Fatalf("Lock: got = %v, %v, want = LockStatusOK, nil", status, err)
	}

	want := []any{7, p9.WriteLock, p9.LockFlagsBlock, uint64(10), uint64(20), "vm"}
	if !reflect.DeepEqual(child.got, want) {
		t.Fatalf("the walked file saw %v, want %v", child.got, want)
	}
	if root.got != nil {
		t.Fatalf("the root saw %v, want no lock", root.got)
	}
}
