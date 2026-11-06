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
	"context"
	"testing"
)

// waitingFile is a ContextLocker whose lock is never free, so a lock waits
// until its context ends. Its other methods are left to the nil File and
// must not be called.
type waitingFile struct {
	File

	waiting chan struct{}
	ended   chan error
}

func (f *waitingFile) GetAttr(AttrMask) (QID, AttrMask, Attr, error) {
	return QID{}, AttrMask{Mode: true}, Attr{Mode: ModeRegular | 0o644}, nil
}

func (f *waitingFile) LockContext(ctx context.Context, _ int, _ LockType, _ LockFlags, _, _ uint64, _ string) (LockStatus, error) {
	f.waiting <- struct{}{}
	<-ctx.Done()
	f.ended <- ctx.Err()
	return LockStatusBlocked, nil
}

func (f *waitingFile) Close() error { return nil }

type waitingAttacher struct{ f *waitingFile }

func (a waitingAttacher) Attach() (File, error) { return a.f, nil }

func newWaitingFile() *waitingFile {
	return &waitingFile{waiting: make(chan struct{}), ended: make(chan error, 1)}
}

func TestFlushEndsALockThatWaits(t *testing.T) {
	f := newWaitingFile()
	w := serveWire(t, waitingAttacher{f})

	w.send(2, &tlock{fid: 1, Type: WriteLock, Flags: LockFlagsBlock})
	<-f.waiting
	w.send(3, &tflush{OldTag: 2})

	if err := <-f.ended; err == nil {
		t.Fatal("LockContext: got = a live context, want = an ended one")
	}
	if tg, m := w.recv(); tg != 2 || m.typ() != msgRlock {
		t.Fatalf("got = %v for tag %d, want = Rlock for tag 2", m, tg)
	}
	if tg, m := w.recv(); tg != 3 || m.typ() != msgRflush {
		t.Fatalf("got = %v for tag %d, want = Rflush for tag 3", m, tg)
	}
}

func TestTheEndOfTheConnectionEndsALockThatWaits(t *testing.T) {
	f := newWaitingFile()
	w := serveWire(t, waitingAttacher{f})

	w.send(2, &tlock{fid: 1, Type: WriteLock, Flags: LockFlagsBlock})
	<-f.waiting
	_ = w.conn.Close()

	// The cleanup of serveWire then waits for Handle to return.
	if err := <-f.ended; err == nil {
		t.Fatal("LockContext: got = a live context, want = an ended one")
	}
}
