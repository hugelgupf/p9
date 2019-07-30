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

package p9

import (
	"testing"

	"github.com/hugelgupf/socketpair"
)

const (
	msgTypeBadEncode = iota + 252
	msgTypeBadDecode
	msgTypeUnregistered
)

func TestSendRecv(t *testing.T) {
	server, client, err := socketpair.TCPPair()
	if err != nil {
		t.Fatalf("socketpair got err %v expected nil", err)
	}
	defer server.Close()
	defer client.Close()

	if err := send(client, Tag(1), &tlopen{}); err != nil {
		t.Fatalf("send got err %v expected nil", err)
	}

	tag, m, err := recv(server, maximumLength, msgRegistry.get)
	if err != nil {
		t.Fatalf("recv got err %v expected nil", err)
	}
	if tag != Tag(1) {
		t.Fatalf("got tag %v expected 1", tag)
	}
	if _, ok := m.(*tlopen); !ok {
		t.Fatalf("got message %v expected *Tlopen", m)
	}
}

// badDecode overruns on decode.
type badDecode struct{}

func (*badDecode) decode(b *buffer) { b.markOverrun() }
func (*badDecode) encode(b *buffer) {}
func (*badDecode) Type() msgType    { return msgTypeBadDecode }
func (*badDecode) String() string   { return "badDecode{}" }

func TestRecvOverrun(t *testing.T) {
	server, client, err := socketpair.TCPPair()
	if err != nil {
		t.Fatalf("socketpair got err %v expected nil", err)
	}
	defer server.Close()
	defer client.Close()

	if err := send(client, Tag(1), &badDecode{}); err != nil {
		t.Fatalf("send got err %v expected nil", err)
	}

	if _, _, err := recv(server, maximumLength, msgRegistry.get); err == nil {
		t.Fatalf("recv got err %v expected ErrSocket{ErrNoValidMessage}", err)
	}
}

// unregistered is not registered on decode.
type unregistered struct{}

func (*unregistered) decode(b *buffer) {}
func (*unregistered) encode(b *buffer) {}
func (*unregistered) Type() msgType    { return msgTypeUnregistered }
func (*unregistered) String() string   { return "unregistered{}" }

func TestRecvInvalidType(t *testing.T) {
	server, client, err := socketpair.TCPPair()
	if err != nil {
		t.Fatalf("socketpair got err %v expected nil", err)
	}
	defer server.Close()
	defer client.Close()

	if err := send(client, Tag(1), &unregistered{}); err != nil {
		t.Fatalf("send got err %v expected nil", err)
	}

	_, _, err = recv(server, maximumLength, msgRegistry.get)
	if _, ok := err.(*ErrInvalidMsgType); !ok {
		t.Fatalf("recv got err %v expected ErrInvalidMsgType", err)
	}
}

func TestRecvClosed(t *testing.T) {
	server, client, err := socketpair.TCPPair()
	if err != nil {
		t.Fatalf("socketpair got err %v expected nil", err)
	}
	defer server.Close()
	client.Close()

	_, _, err = recv(server, maximumLength, msgRegistry.get)
	if err == nil {
		t.Fatalf("got err nil expected non-nil")
	}
	if _, ok := err.(ErrSocket); !ok {
		t.Fatalf("got err %v expected ErrSocket", err)
	}
}

func DISABLEDTestSendClosed(t *testing.T) {
	server, client, err := socketpair.TCPPair()
	if err != nil {
		t.Fatalf("socketpair got err %v expected nil", err)
	}
	server.Close()
	defer client.Close()

	err = send(client, Tag(1), &tlopen{})
	if err == nil {
		t.Fatalf("send got err nil expected non-nil")
	}
	if _, ok := err.(ErrSocket); !ok {
		t.Fatalf("got err %v expected ErrSocket", err)
	}
}

func BenchmarkSendRecv(b *testing.B) {
	server, client, err := socketpair.TCPPair()
	if err != nil {
		b.Fatalf("socketpair got err %v expected nil", err)
	}
	defer server.Close()
	defer client.Close()

	// Exchange Rflush messages since these contain no data and therefore incur
	// no additional marshaling overhead.
	go func() {
		for i := 0; i < b.N; i++ {
			tag, m, err := recv(server, maximumLength, msgRegistry.get)
			if err != nil {
				b.Fatalf("recv got err %v expected nil", err)
			}
			if tag != Tag(1) {
				b.Fatalf("got tag %v expected 1", tag)
			}
			if _, ok := m.(*rflush); !ok {
				b.Fatalf("got message %T expected *Rflush", m)
			}
			if err := send(server, Tag(2), &rflush{}); err != nil {
				b.Fatalf("send got err %v expected nil", err)
			}
		}
	}()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := send(client, Tag(1), &rflush{}); err != nil {
			b.Fatalf("send got err %v expected nil", err)
		}
		tag, m, err := recv(client, maximumLength, msgRegistry.get)
		if err != nil {
			b.Fatalf("recv got err %v expected nil", err)
		}
		if tag != Tag(2) {
			b.Fatalf("got tag %v expected 2", tag)
		}
		if _, ok := m.(*rflush); !ok {
			b.Fatalf("got message %v expected *Rflush", m)
		}
	}
}

func init() {
	msgRegistry.register(msgTypeBadDecode, func() message { return &badDecode{} })
}
