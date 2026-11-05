// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package p9

import (
	"errors"
	"net"
	"testing"

	"github.com/hugelgupf/p9/linux"
	"github.com/u-root/uio/ulog/ulogtest"
)

// lyingXattrServer answers just enough of the protocol to get a client to
// Txattrwalk, then reports size for the attribute however large the caller
// asked.
//
// It is hand-rolled rather than built on p9.NewServer because the real server
// enforces maximumLength on this very field, so it cannot produce the reply
// under test. That is the point: the client must not trust a peer to have
// checked.
func lyingXattrServer(t *testing.T, conn net.Conn, size uint64) {
	t.Helper()

	l := ulogtest.Logger{TB: t}
	for {
		tg, m, err := recv(l, conn, maximumLength, msgDotLRegistry.get)
		if err != nil {
			// The client hung up, which is how this ends.
			return
		}

		var reply message
		switch msg := m.(type) {
		case *tversion:
			reply = &rversion{MSize: msg.MSize, Version: msg.Version}
		case *tattach:
			reply = &rattach{QID: QID{Type: TypeRegular, Path: 1}}
		case *txattrwalk:
			reply = &rxattrwalk{Size: size}
		case *tclunk:
			reply = &rclunk{}
		default:
			reply = &rlerror{Error: uint32(linux.ENOSYS)}
		}
		if err := send(l, conn, tg, reply); err != nil {
			return
		}
	}
}

// A server may report any size it likes for an extended attribute, so the
// client must bound it before allocating.
//
// Size is a uint64 off the wire and describes the attribute rather than the
// message carrying it, so the transport's length check never constrains it.
// Without a bound, a peer reporting 2^64-1 makes the client attempt a
// 16-exabyte allocation, which is a denial of service that costs the server
// one small message.
func TestGetXattrBoundsTheSizeAServerReports(t *testing.T) {
	for _, tt := range []struct {
		name string
		size uint64
	}{
		{"the largest possible uint64", ^uint64(0)},
		{"one byte over the maximum", uint64(maximumLength) + 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f, cleanup := serveLying(t, tt.size)
			defer cleanup()

			got, err := f.GetXattr("user.whatever")
			if !errors.Is(err, linux.EINVAL) {
				t.Errorf("GetXattr with a reported size of %d: got err = %v, want = EINVAL", tt.size, err)
			}
			if got != nil {
				t.Errorf("GetXattr with a reported size of %d: got = %d bytes, want = nil", tt.size, len(got))
			}
		})
	}
}

// The bound must not reject a size a conforming server can actually produce.
// maximumLength is exactly what the server will serve, so it has to be
// allowed rather than refused by an off-by-one.
func TestGetXattrAllowsTheLargestServableSize(t *testing.T) {
	f, cleanup := serveLying(t, uint64(maximumLength))
	defer cleanup()

	// This server answers nothing but the walk, so the Tread that follows
	// comes back ENOSYS. Asserting that rather than "not EINVAL" says two
	// things at once: the bound let maximumLength through, and
	// xattrWalkRead went on to read, instead of failing earlier for some
	// unrelated reason and passing by accident.
	if _, err := f.GetXattr("user.whatever"); !errors.Is(err, linux.ENOSYS) {
		t.Errorf("GetXattr at exactly maximumLength: got err = %v, want ENOSYS from the Tread", err)
	}
}

// serveLying stands up a client against a server reporting size for every
// attribute, and returns a cleanup that closes both and waits for the server
// goroutine.
//
// The waiting is the point. Client files carry a finalizer that clunks them
// (client_file.go, newFile), so a GC during the test can send a Tclunk after
// the test body returns. If the server is still running it calls t.Logf on a
// finished test, which panics with "Log in goroutine after Test... completed"
// and names neither this test nor the finalizer.
func serveLying(t *testing.T, size uint64) (File, func()) {
	t.Helper()

	srv, cli := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		lyingXattrServer(t, srv, size)
	}()

	c, err := NewClient(cli)
	if err != nil {
		_ = srv.Close()
		<-done
		t.Fatalf("NewClient: got = %v, want = nil", err)
	}
	f, err := c.Attach("")
	if err != nil {
		_ = c.Close()
		_ = srv.Close()
		<-done
		t.Fatalf("Attach: got = %v, want = nil", err)
	}
	return f, func() {
		_ = f.Close()
		_ = c.Close()
		_ = srv.Close()
		<-done
	}
}
