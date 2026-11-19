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
	"bytes"
	"sync"
	"sync/atomic"
	"testing"
)

// xattrSink records the value it is last given by SetXattr. File methods the
// test never reaches are left to the embedded nil File.
type xattrSink struct {
	File

	mu    sync.Mutex
	value []byte
}

func (*xattrSink) Close() error { return nil }

func (f *xattrSink) SetXattr(_ string, data []byte, _ XattrFlags) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.value = append([]byte(nil), data...)
	return nil
}

// Twrites to a Txattrcreate fid append to a buffer owned by that fid, and a
// client may send several of them at once. The server has to serialize them:
// of concurrent Twrites at the same offset exactly one may be accepted, and
// the value handed to SetXattr at Tclunk must be the one that was.
//
// Run with -race to catch the unsynchronized append directly.
func TestTwriteXattrCreateConcurrent(t *testing.T) {
	value := []byte("0123456789abcdef")
	const writers = 8

	for round := 0; round < 100; round++ {
		s := NewServer(nil)
		cs := &connState{server: s, fids: make(map[fid]*fidRef)}
		sink := &xattrSink{}
		cs.InsertFID(1, &fidRef{
			server:   s,
			file:     sink,
			pathNode: s.pathTree,
			pendingXattr: pendingXattr{
				op:   xattrCreate,
				name: "user.test",
				size: uint64(len(value)),
			},
		})

		var (
			start    = make(chan struct{})
			wg       sync.WaitGroup
			accepted atomic.Int32
		)
		for i := 0; i < writers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if _, ok := (&twrite{fid: 1, Offset: 0, Data: value}).handle(cs).(*rwrite); ok {
					accepted.Add(1)
				}
			}()
		}
		close(start)
		wg.Wait()

		if got := accepted.Load(); got != 1 {
			t.Fatalf("round %d: %d concurrent Twrites at offset 0 accepted, want 1", round, got)
		}
		if r, ok := (&tclunk{fid: 1}).handle(cs).(*rclunk); !ok {
			t.Fatalf("round %d: Tclunk = %v, want Rclunk", round, r)
		}
		if !bytes.Equal(sink.value, value) {
			t.Fatalf("round %d: SetXattr got %q, want %q", round, sink.value, value)
		}
	}
}
