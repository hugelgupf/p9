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
	"net"
	"testing"

	"github.com/u-root/uio/ulog"
)

// heldFile is a File whose Lock holds each request until the test lets it
// go. Its other methods are left to the nil File and must not be called.
type heldFile struct {
	File

	locked  chan struct{}
	release chan struct{}
}

func (f *heldFile) GetAttr(AttrMask) (QID, AttrMask, Attr, error) {
	return QID{}, AttrMask{Mode: true}, Attr{Mode: ModeRegular | 0o644}, nil
}

func (f *heldFile) Lock(int, LockType, LockFlags, uint64, uint64, string) (LockStatus, error) {
	f.locked <- struct{}{}
	<-f.release
	return LockStatusOK, nil
}

func (f *heldFile) Close() error { return nil }

type heldAttacher struct{ f *heldFile }

func (a heldAttacher) Attach() (File, error) { return a.f, nil }

// wire is the client's end of a connection to a server, which speaks raw
// messages, as the Client never sends Tflush.
type wire struct {
	t    *testing.T
	conn net.Conn
}

func serveWire(t *testing.T, a Attacher) *wire {
	server, client := net.Pipe()
	done := make(chan struct{})
	go func() {
		_ = NewServer(a).Handle(server, server)
		close(done)
	}()
	t.Cleanup(func() {
		_ = client.Close()
		<-done
	})

	w := &wire{t: t, conn: client}
	w.send(1, &tversion{MSize: maximumLength, Version: "9P2000.L"})
	if _, m := w.recv(); m.typ() != msgRversion {
		t.Fatalf("Tversion: got = %v, want = Rversion", m)
	}
	w.send(1, &tattach{fid: 1, Auth: tauth{Authenticationfid: noFID}})
	if _, m := w.recv(); m.typ() != msgRattach {
		t.Fatalf("Tattach: got = %v, want = Rattach", m)
	}
	return w
}

func (w *wire) send(tg tag, m message) {
	w.t.Helper()
	if err := send(ulog.Null, w.conn, tg, m); err != nil {
		w.t.Fatalf("send(%v): got = %v, want = nil", m, err)
	}
}

func (w *wire) recv() (tag, message) {
	w.t.Helper()
	tg, m, err := recv(ulog.Null, w.conn, maximumLength, msgDotLRegistry.get)
	if err != nil {
		w.t.Fatalf("recv: got = %v, want = nil", err)
	}
	return tg, m
}

func TestFlushAnswersAfterTheRequestItFlushes(t *testing.T) {
	f := &heldFile{locked: make(chan struct{}), release: make(chan struct{})}
	w := serveWire(t, heldAttacher{f})

	// The window in which a Tflush could overtake its request is short,
	// so try it often.
	for i := 0; i < 1000; i++ {
		w.send(2, &tlock{fid: 1, Type: WriteLock})
		w.send(3, &tflush{OldTag: 2})

		<-f.locked
		f.release <- struct{}{}

		if tg, _ := w.recv(); tg != 2 {
			t.Fatalf("try %d: got = the answer to tag %d first, want = the answer to the flushed request", i, tg)
		}
		if tg, m := w.recv(); tg != 3 || m.typ() != msgRflush {
			t.Fatalf("try %d: got = %v for tag %d, want = Rflush for tag 3", i, m, tg)
		}
	}
}
