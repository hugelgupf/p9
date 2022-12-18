//go:build linux
// +build linux

package p9

import (
	"testing"

	"github.com/hugelgupf/socketpair"
	"github.com/u-root/uio/ulog/ulogtest"
)

func BenchmarkSendRecvTCP(b *testing.B) {
	server, client, err := socketpair.TCPPair()
	if err != nil {
		b.Fatalf("socketpair got err %v expected nil", err)
	}
	defer server.Close()
	defer client.Close()

	l := ulogtest.Logger{TB: b}
	// Exchange Rflush messages since these contain no data and therefore incur
	// no additional marshaling overhead.
	go func() {
		for i := 0; i < b.N; i++ {
			t, m, err := recv(l, server, maximumLength, msgDotLRegistry.get)
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
		t, m, err := recv(l, client, maximumLength, msgDotLRegistry.get)
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
