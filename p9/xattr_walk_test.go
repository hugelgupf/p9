// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package p9

import (
	"errors"
	"io"
	"maps"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hugelgupf/p9/linux"
	"github.com/u-root/uio/ulog/ulogtest"
)

type stubXattrFile struct {
	xattrWalkFile

	mu          sync.Mutex
	mode        FileMode
	children    map[string]*stubXattrFile
	content     []byte
	xattrs      map[string][]byte
	opened      bool
	lastClone   *stubXattrFile
	closedCount atomic.Int32
}

func newStubXattrFile(content string, xattrs map[string][]byte) *stubXattrFile {
	if xattrs == nil {
		xattrs = make(map[string][]byte)
	}
	return &stubXattrFile{
		mode:    ModeRegular | 0o644,
		content: []byte(content),
		xattrs:  xattrs,
	}
}

func (f *stubXattrFile) clone() *stubXattrFile {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make(map[string][]byte, len(f.xattrs))
	for k, v := range f.xattrs {
		cp[k] = slices.Clone(v)
	}
	c := &stubXattrFile{
		mode:     f.mode,
		children: maps.Clone(f.children),
		content:  slices.Clone(f.content),
		xattrs:   cp,
	}
	f.lastClone = c
	return c
}

func (f *stubXattrFile) qid() QID {
	if f.mode.IsDir() {
		return QID{Type: TypeDir, Path: 2}
	}
	return QID{Type: TypeRegular, Path: 1}
}

func (f *stubXattrFile) Walk(names []string) ([]QID, File, error) {
	if len(names) == 0 {
		return []QID{f.qid()}, f.clone(), nil
	}
	f.mu.Lock()
	child, ok := f.children[names[0]]
	f.mu.Unlock()
	if len(names) == 1 && ok {
		return []QID{child.qid()}, child.clone(), nil
	}
	return nil, nil, linux.ENOENT
}

func (f *stubXattrFile) GetAttr(AttrMask) (QID, AttrMask, Attr, error) {
	return f.qid(), AttrMask{Mode: true}, Attr{Mode: f.mode}, nil
}

func (f *stubXattrFile) Close() error {
	f.closedCount.Add(1)
	return nil
}

func (f *stubXattrFile) Open(OpenFlags) (QID, uint32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closedCount.Load() > 0 {
		return QID{}, 0, linux.EBADF
	}
	f.opened = true
	return f.qid(), 0, nil
}

func (f *stubXattrFile) ReadAt(p []byte, offset int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.opened || f.closedCount.Load() > 0 {
		return 0, linux.EBADF
	}
	if offset >= int64(len(f.content)) {
		return 0, io.EOF
	}
	n := copy(p, f.content[offset:])
	if offset+int64(n) >= int64(len(f.content)) {
		return n, io.EOF
	}
	return n, nil
}

func (f *stubXattrFile) WriteAt(p []byte, offset int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.opened || f.closedCount.Load() > 0 {
		return 0, linux.EBADF
	}
	end := int(offset) + len(p)
	if end > len(f.content) {
		grown := make([]byte, end)
		copy(grown, f.content)
		f.content = grown
	}
	copy(f.content[offset:], p)
	return len(p), nil
}

func (f *stubXattrFile) SetXattr(attr string, data []byte, _ XattrFlags) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closedCount.Load() > 0 {
		return linux.EBADF
	}
	f.xattrs[attr] = slices.Clone(data)
	return nil
}

func (f *stubXattrFile) GetXattr(attr string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closedCount.Load() > 0 {
		return nil, linux.EBADF
	}
	v, ok := f.xattrs[attr]
	if !ok {
		return nil, linux.ENODATA
	}
	return slices.Clone(v), nil
}

func (f *stubXattrFile) ListXattrs() ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closedCount.Load() > 0 {
		return nil, linux.EBADF
	}
	if len(f.xattrs) == 0 {
		return nil, nil
	}
	return slices.Sorted(maps.Keys(f.xattrs)), nil
}

func (f *stubXattrFile) FSync() error {
	if f.closedCount.Load() > 0 {
		return linux.EBADF
	}
	return nil
}

func (f *stubXattrFile) Link(target File, _ string) error {
	// Match concrete backends like localfs.(*Local).Link that type-assert
	// target to their own File implementation.
	_ = target.(*stubXattrFile)
	return nil
}

type stubAttacher struct {
	root *stubXattrFile
}

func (a stubAttacher) Attach() (File, error) { return a.root, nil }

func serveStubXattr(t *testing.T, root *stubXattrFile) File {
	t.Helper()
	srv, cli := net.Pipe()
	s := NewServer(stubAttacher{root: root}, WithServerLogger(ulogtest.Logger{TB: t}))
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

	c, err := NewClient(cli)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	f, err := c.Attach("")
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// TestTxattrwalkClunkPreservesParentFile verifies that clunking the xattr FID
// created by Txattrwalk does not close the parent FID's backing File.
func TestTxattrwalkClunkPreservesParentFile(t *testing.T) {
	root := newStubXattrFile("initial", map[string][]byte{
		"user.tag": []byte("v1"),
	})
	f := serveStubXattr(t, root)

	if _, _, err := f.Open(ReadWrite); err != nil {
		t.Fatalf("Open: %v", err)
	}

	gotAttr, err := f.GetXattr("user.tag")
	if err != nil {
		t.Fatalf("GetXattr: %v", err)
	}
	if string(gotAttr) != "v1" {
		t.Fatalf("GetXattr = %q, want %q", gotAttr, "v1")
	}

	names, err := f.ListXattrs()
	if err != nil {
		t.Fatalf("ListXattrs: %v", err)
	}
	if !slices.Equal(names, []string{"user.tag"}) {
		t.Fatalf("ListXattrs = %v, want [user.tag]", names)
	}

	if got := root.closedCount.Load(); got != 0 {
		t.Fatalf("root closedCount while still open = %d, want 0", got)
	}

	if n, err := f.WriteAt([]byte("-updated"), 7); err != nil || n != 8 {
		t.Fatalf("WriteAt after xattr walk = (%d, %v), want (8, nil)", n, err)
	}
	if err := f.FSync(); err != nil {
		t.Fatalf("FSync after xattr walk = %v, want nil", err)
	}
	buf := make([]byte, 32)
	n, err := f.ReadAt(buf, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("ReadAt after xattr walk = %v", err)
	}
	if string(buf[:n]) != "initial-updated" {
		t.Fatalf("ReadAt = %q, want %q", buf[:n], "initial-updated")
	}

	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := root.closedCount.Load(); got != 1 {
		t.Fatalf("root closedCount after Close = %d, want 1", got)
	}
}

// TestTxattrwalkProtocolEdgeCases exercises raw 9P2000.L Txattrwalk wire
// behavior:
//   - empty xattr list reports Rxattrwalk{Size: 0}, not 1;
//   - Txattrwalk with fid == newFID on an opened FID returns EBUSY;
//   - Txattrwalk, Txattrcreate, or Tlink on an xattrWalk FID returns EINVAL;
//   - Tread on an xattrWalk FID with overflowing Offset+Count returns EINVAL;
//   - Txattrwalk with fid == newFID on an unopened FID closes the replaced
//     file once rather than double-closing on clunk.
func TestTxattrwalkProtocolEdgeCases(t *testing.T) {
	child := newStubXattrFile("hello", nil)
	rootDir := &stubXattrFile{
		mode:     ModeDirectory | 0o755,
		children: map[string]*stubXattrFile{"file": child},
		xattrs:   make(map[string][]byte),
	}

	srv, cli := net.Pipe()
	s := NewServer(stubAttacher{root: rootDir}, WithServerLogger(ulogtest.Logger{TB: t}))
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

	log := ulogtest.Logger{TB: t}
	roundTrip := func(req message) message {
		t.Helper()
		if err := send(log, cli, tag(1), req); err != nil {
			t.Fatalf("send(%v): %v", req, err)
		}
		_, resp, err := recv(log, cli, maximumLength, msgDotLRegistry.get)
		if err != nil {
			t.Fatalf("recv(%v): %v", req, err)
		}
		return resp
	}

	if resp := roundTrip(&tversion{MSize: DefaultMessageSize, Version: "9P2000.L"}); resp.typ() != msgRversion {
		t.Fatalf("Tversion got %v", resp)
	}
	if resp := roundTrip(&tattach{fid: 0, Auth: tauth{Authenticationfid: noFID, UID: NoUID}}); resp.typ() != msgRattach {
		t.Fatalf("Tattach got %v", resp)
	}
	if resp := roundTrip(&twalk{fid: 0, newFID: 1, Names: []string{"file"}}); resp.typ() != msgRwalk {
		t.Fatalf("Twalk(file) got %v", resp)
	}
	walkedFile := child.lastClone

	// Empty xattr list must report Size: 0 on the wire, not Size: 1.
	resp := roundTrip(&txattrwalk{fid: 1, newFID: 2, Name: ""})
	rxw, ok := resp.(*rxattrwalk)
	if !ok {
		t.Fatalf("Txattrwalk empty list got %T (%v), want *rxattrwalk", resp, resp)
	}
	if rxw.Size != 0 {
		t.Errorf("Rxattrwalk.Size on file with 0 xattrs = %d, want 0", rxw.Size)
	}

	// Txattrwalk on an xattrWalk FID must fail with EINVAL.
	resp = roundTrip(&txattrwalk{fid: 2, newFID: 3, Name: ""})
	if rerr, ok := resp.(*rlerror); !ok || linux.Errno(rerr.Error) != linux.EINVAL {
		t.Errorf("Txattrwalk on xattrWalk FID = %v, want EINVAL", resp)
	}

	// Txattrcreate on an xattrWalk FID must fail with EINVAL.
	resp = roundTrip(&txattrcreate{fid: 2, Name: "user.bad", AttrSize: 0})
	if rerr, ok := resp.(*rlerror); !ok || linux.Errno(rerr.Error) != linux.EINVAL {
		t.Errorf("Txattrcreate on xattrWalk FID = %v, want EINVAL", resp)
	}

	// Tlink targeting an xattrWalk FID must fail with EINVAL, not panic.
	resp = roundTrip(&tlink{Directory: 0, Target: 2, Name: "hardlink"})
	if rerr, ok := resp.(*rlerror); !ok || linux.Errno(rerr.Error) != linux.EINVAL {
		t.Errorf("Tlink targeting xattrWalk FID = %v, want EINVAL", resp)
	}

	// Tread with overflowing Offset+Count must return EINVAL, not EFAULT.
	walkedFile.SetXattr("user.k", []byte("val"), 0)
	if resp := roundTrip(&txattrwalk{fid: 1, newFID: 4, Name: "user.k"}); resp.typ() != msgRxattrwalk {
		t.Fatalf("Txattrwalk(user.k) = %v", resp)
	}
	resp = roundTrip(&tread{fid: 4, Offset: ^uint64(0), Count: 2})
	if rerr, ok := resp.(*rlerror); !ok || linux.Errno(rerr.Error) != linux.EINVAL {
		t.Errorf("Tread overflow on xattrWalk FID = %v, want EINVAL", resp)
	}
	_ = roundTrip(&tclunk{fid: 2})
	_ = roundTrip(&tclunk{fid: 4})

	// Clone fid 1 -> fid 5 (unopened) and walk xattrs in place.
	if resp := roundTrip(&twalk{fid: 1, newFID: 5}); resp.typ() != msgRwalk {
		t.Fatalf("Twalk clone = %v", resp)
	}
	cloned := walkedFile.lastClone
	if resp := roundTrip(&txattrwalk{fid: 5, newFID: 5, Name: "user.k"}); resp.typ() != msgRxattrwalk {
		t.Fatalf("Txattrwalk(fid==newFID unopened) = %v", resp)
	}
	if got := cloned.closedCount.Load(); got != 1 {
		t.Errorf("cloned closedCount after in-place Txattrwalk = %d, want 1", got)
	}
	if resp := roundTrip(&tclunk{fid: 5}); resp.typ() != msgRclunk {
		t.Fatalf("Tclunk(5) = %v", resp)
	}
	if got := cloned.closedCount.Load(); got != 1 {
		t.Errorf("cloned closedCount after Tclunk = %d, want 1", got)
	}

	// Open fid 1, then verify Txattrwalk with fid == newFID returns EBUSY.
	if resp := roundTrip(&tlopen{fid: 1, Flags: ReadOnly}); resp.typ() != msgRlopen {
		t.Fatalf("Tlopen(1) = %v", resp)
	}
	resp = roundTrip(&txattrwalk{fid: 1, newFID: 1, Name: "user.k"})
	if rerr, ok := resp.(*rlerror); !ok || linux.Errno(rerr.Error) != linux.EBUSY {
		t.Errorf("Txattrwalk(fid==newFID opened) = %v, want EBUSY", resp)
	}
	if got := walkedFile.closedCount.Load(); got != 0 {
		t.Errorf("walkedFile closedCount while fid 1 open = %d, want 0", got)
	}
}

// TestXattrWalkFileMethods verifies the sentinel File contract: Close is a
// no-op (so clunking an xattrWalk FID never closes the parent File), Renamed
// is a no-op, WalkGetAttr defers to ENOSYS, and every other method returns
// EINVAL rather than panicking or silently succeeding.
func TestXattrWalkFileMethods(t *testing.T) {
	var f xattrWalkFile
	if err := f.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
	f.Renamed(nil, "x")
	if _, _, _, _, err := f.WalkGetAttr(nil); !errors.Is(err, linux.ENOSYS) {
		t.Errorf("WalkGetAttr() err = %v, want ENOSYS", err)
	}
	checks := []struct {
		name string
		err  error
	}{
		{"Walk", func() error { _, _, err := f.Walk(nil); return err }()},
		{"StatFS", func() error { _, err := f.StatFS(); return err }()},
		{"GetAttr", func() error { _, _, _, err := f.GetAttr(AttrMask{}); return err }()},
		{"SetAttr", f.SetAttr(SetAttrMask{}, SetAttr{})},
		{"Open", func() error { _, _, err := f.Open(ReadOnly); return err }()},
		{"ReadAt", func() error { _, err := f.ReadAt(nil, 0); return err }()},
		{"WriteAt", func() error { _, err := f.WriteAt(nil, 0); return err }()},
		{"SetXattr", f.SetXattr("a", nil, 0)},
		{"GetXattr", func() error { _, err := f.GetXattr("a"); return err }()},
		{"ListXattrs", func() error { _, err := f.ListXattrs(); return err }()},
		{"RemoveXattr", f.RemoveXattr("a")},
		{"FSync", f.FSync()},
		{"Lock", func() error { _, err := f.Lock(0, ReadLock, 0, 0, 0, ""); return err }()},
		{"Create", func() error { _, _, _, err := f.Create("a", 0, 0, 0, 0); return err }()},
		{"Mkdir", func() error { _, err := f.Mkdir("a", 0, 0, 0); return err }()},
		{"Symlink", func() error { _, err := f.Symlink("a", "b", 0, 0); return err }()},
		{"Link", f.Link(nil, "a")},
		{"Mknod", func() error { _, err := f.Mknod("a", 0, 0, 0, 0, 0); return err }()},
		{"Rename", f.Rename(nil, "a")},
		{"RenameAt", f.RenameAt("a", nil, "b")},
		{"UnlinkAt", f.UnlinkAt("a", 0)},
		{"Readdir", func() error { _, err := f.Readdir(0, 0); return err }()},
		{"Readlink", func() error { _, err := f.Readlink(); return err }()},
	}
	for _, tc := range checks {
		if !errors.Is(tc.err, linux.EINVAL) {
			t.Errorf("%s() err = %v, want EINVAL", tc.name, tc.err)
		}
	}
}
