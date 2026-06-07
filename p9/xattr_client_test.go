// Copyright 2024 The gVisor Authors.
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
	"net"
	"sort"
	"testing"

	"github.com/hugelgupf/p9/fsimpl/templatefs"
	"github.com/hugelgupf/p9/linux"
	"github.com/hugelgupf/p9/p9"
)

// xattrFile is a single-node file whose extended attributes are fixed at
// construction. A missing attribute reports linux.ENODATA, matching getxattr(2),
// so the test can assert the server propagates that errno (rather than
// flattening it) and the client surfaces it.
type xattrFile struct {
	templatefs.NoopFile
	xattrs map[string][]byte
}

func (f *xattrFile) Walk(names []string) ([]p9.QID, p9.File, error) {
	if len(names) == 0 {
		return []p9.QID{f.qid()}, f, nil
	}
	return nil, nil, linux.ENOENT
}

func (f *xattrFile) GetAttr(p9.AttrMask) (p9.QID, p9.AttrMask, p9.Attr, error) {
	return f.qid(), p9.AttrMask{Mode: true}, p9.Attr{Mode: p9.ModeRegular | 0o644}, nil
}

func (f *xattrFile) GetXattr(attr string) ([]byte, error) {
	v, ok := f.xattrs[attr]
	if !ok {
		return nil, linux.ENODATA
	}
	return append([]byte(nil), v...), nil
}

func (f *xattrFile) ListXattrs() ([]string, error) {
	names := make([]string, 0, len(f.xattrs))
	for k := range f.xattrs {
		names = append(names, k)
	}
	sort.Strings(names)
	return names, nil
}

func (f *xattrFile) qid() p9.QID { return p9.QID{Type: p9.TypeRegular, Path: 1} }

type xattrAttacher struct{ root *xattrFile }

func (a xattrAttacher) Attach() (p9.File, error) { return a.root, nil }

// serveXattr stands up an in-process p9 server backed by root over a net.Pipe
// and returns a connected client. The returned cleanup tears both down.
func serveXattr(t *testing.T, root *xattrFile) (p9.File, func()) {
	t.Helper()
	srv, cli := net.Pipe()
	s := p9.NewServer(xattrAttacher{root: root})
	done := make(chan struct{})
	go func() {
		_ = s.Handle(srv, srv)
		close(done)
	}()
	c, err := p9.NewClient(cli)
	if err != nil {
		_ = srv.Close()
		_ = cli.Close()
		t.Fatalf("NewClient: got = %v, want = nil", err)
	}
	attached, err := c.Attach("")
	if err != nil {
		_ = srv.Close()
		_ = cli.Close()
		t.Fatalf("Attach: got = %v, want = nil", err)
	}
	return attached, func() {
		_ = attached.Close()
		_ = cli.Close()
		_ = srv.Close()
		<-done
	}
}

// TestClientGetXattr is the regression test for the two library gaps that kept
// a guest's getxattr from working: clientFile.GetXattr was a stub returning
// ENOSYS, and the server's txattrwalk handler flattened every GetXattr error to
// EINVAL. After the fix, a present attribute round-trips and a missing one
// surfaces as ENODATA — what getxattr(2) callers (the kernel probing
// security.capability/ACLs, getfattr) expect.
func TestClientGetXattr(t *testing.T) {
	root := &xattrFile{xattrs: map[string][]byte{
		"user.greeting": []byte("hello xattr"),
		"user.empty":    {},
	}}
	f, cleanup := serveXattr(t, root)
	defer cleanup()

	t.Run("Present", func(t *testing.T) {
		got, err := f.GetXattr("user.greeting")
		if err != nil {
			t.Fatalf("GetXattr present: got = %v, want = nil", err)
		}
		if string(got) != "hello xattr" {
			t.Errorf("GetXattr present: got = %q, want = %q", got, "hello xattr")
		}
	})

	t.Run("Empty", func(t *testing.T) {
		got, err := f.GetXattr("user.empty")
		if err != nil {
			t.Fatalf("GetXattr empty: got = %v, want = nil", err)
		}
		if len(got) != 0 {
			t.Errorf("GetXattr empty: got = %q, want = empty", got)
		}
	})

	t.Run("MissingIsENODATA", func(t *testing.T) {
		_, err := f.GetXattr("user.does-not-exist")
		if !errors.Is(err, linux.ENODATA) {
			t.Errorf("GetXattr missing: got = %v, want = ENODATA", err)
		}
	})
}

// TestClientListXattrs round-trips the attribute name list through the client.
func TestClientListXattrs(t *testing.T) {
	root := &xattrFile{xattrs: map[string][]byte{
		"user.a": []byte("1"),
		"user.b": []byte("2"),
	}}
	f, cleanup := serveXattr(t, root)
	defer cleanup()

	got, err := f.ListXattrs()
	if err != nil {
		t.Fatalf("ListXattrs: got = %v, want = nil", err)
	}
	sort.Strings(got)
	if len(got) != 2 || got[0] != "user.a" || got[1] != "user.b" {
		t.Errorf("ListXattrs: got = %v, want = [user.a user.b]", got)
	}
}
