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
	"github.com/u-root/u-root/pkg/ulog/ulogtest"
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

	l := ulogtest.Logger{t}
	if err := send(l, client, tag(1), &tlopen{}); err != nil {
		t.Fatalf("send got err %v expected nil", err)
	}

	tagg, m, err := recv(l, server, maximumLength, msgRegistry.get)
	if err != nil {
		t.Fatalf("recv got err %v expected nil", err)
	}
	if tagg != tag(1) {
		t.Fatalf("got tag %v expected 1", tagg)
	}
	if _, ok := m.(*tlopen); !ok {
		t.Fatalf("got message %v expected *Tlopen", m)
	}
}

// badDecode overruns on decode.
type badDecode struct{}

func (*badDecode) decode(b *buffer) { b.markOverrun() }
func (*badDecode) encode(b *buffer) {}
func (*badDecode) typ() msgType     { return msgTypeBadDecode }
func (*badDecode) String() string   { return "badDecode{}" }

func TestRecvOverrun(t *testing.T) {
	server, client, err := socketpair.TCPPair()
	if err != nil {
		t.Fatalf("socketpair got err %v expected nil", err)
	}
	defer server.Close()
	defer client.Close()

	l := ulogtest.Logger{t}
	if err := send(l, client, tag(1), &badDecode{}); err != nil {
		t.Fatalf("send got err %v expected nil", err)
	}

	if _, _, err := recv(l, server, maximumLength, msgRegistry.get); err == nil {
		t.Fatalf("recv got err %v expected ErrSocket{ErrNoValidMessage}", err)
	}
}

// unregistered is not registered on decode.
type unregistered struct{}

func (*unregistered) decode(b *buffer) {}
func (*unregistered) encode(b *buffer) {}
func (*unregistered) typ() msgType     { return msgTypeUnregistered }
func (*unregistered) String() string   { return "unregistered{}" }

func TestRecvInvalidType(t *testing.T) {
	server, client, err := socketpair.TCPPair()
	if err != nil {
		t.Fatalf("socketpair got err %v expected nil", err)
	}
	defer server.Close()
	defer client.Close()

	l := ulogtest.Logger{t}
	if err := send(l, client, tag(1), &unregistered{}); err != nil {
		t.Fatalf("send got err %v expected nil", err)
	}

	_, _, err = recv(l, server, maximumLength, msgRegistry.get)
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

	l := ulogtest.Logger{t}
	_, _, err = recv(l, server, maximumLength, msgRegistry.get)
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

	l := ulogtest.Logger{t}
	err = send(l, client, tag(1), &tlopen{})
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

	l := ulogtest.Logger{b}
	// Exchange Rflush messages since these contain no data and therefore incur
	// no additional marshaling overhead.
	go func() {
		for i := 0; i < b.N; i++ {
			t, m, err := recv(l, server, maximumLength, msgRegistry.get)
			if err != nil {
				b.Fatalf("recv got err %v expected nil", err)
			}
			if t != tag(1) {
				b.Fatalf("got tag %v expected 1", t)
			}
			if _, ok := m.(*rflush); !ok {
				b.Fatalf("got message %T expected *Rflush", m)
			}
			if err := send(l, server, tag(2), &rflush{}); err != nil {
				b.Fatalf("send got err %v expected nil", err)
			}
		}
	}()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := send(l, client, tag(1), &rflush{}); err != nil {
			b.Fatalf("send got err %v expected nil", err)
		}
		t, m, err := recv(l, client, maximumLength, msgRegistry.get)
		if err != nil {
			b.Fatalf("recv got err %v expected nil", err)
		}
		if t != tag(2) {
			b.Fatalf("got tag %v expected 2", t)
		}
		if _, ok := m.(*rflush); !ok {
			b.Fatalf("got message %v expected *Rflush", m)
		}
	}
}

func init() {
	msgRegistry.register(msgTypeBadDecode, func() message { return &badDecode{} })
}
