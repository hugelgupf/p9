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

package vecnet

import (
	"bytes"
	"io"
	"net"
	"strings"
	"testing"
)

type chunkReader struct {
	r io.Reader
	n int
}

func (r chunkReader) Read(p []byte) (int, error) {
	return r.r.Read(p[:r.n])
}

func TestReadFromSanity(t *testing.T) {
	bufs := make(Buffers, 2)
	bufs[0] = make([]byte, 10)
	bufs[1] = make([]byte, 5)

	s := "0123456789ab"
	n, err := bufs.ReadFrom(chunkReader{r: strings.NewReader(s), n: 2})
	if err != io.EOF {
		t.Errorf("ReadFrom() = %v, want %v", err, io.EOF)
	}
	if int(n) != len(s) {
		t.Errorf("ReadFrom() = %d bytes read, want %d", n, len(s))
	}
	if s1, s2 := string(bufs[0]), string(bufs[1][:2]); s1 != s[:10] || s2 != s[10:] {
		t.Errorf("ReadFrom() = (%v, %#v), want (%v, %#v)", s1, s2, s[:10], s[10:])
	}
}

func TestReadFromNetwork(t *testing.T) {
	testCloser := func(closer io.Closer) {
		t.Helper()
		if err := closer.Close(); err != nil {
			t.Error(err)
		}
	}

	readerConn, readerAddr := newLocalListener(t)
	defer testCloser(readerConn)
	writerConn := dialListener(t, readerAddr)
	defer testCloser(writerConn)

	var (
		expected = Buffers{
			[]byte("ABCDEFGHIJKLMNOPQRSTUVWXYZ"),
			[]byte("0123456789"),
			[]byte("abcdefghijklmnopqrstuvwxyz"),
		}
		received = func() (received Buffers) {
			received = make(Buffers, len(expected))
			for i := range received {
				received[i] = make([]byte, len(expected[i]))
			}
			return
		}()
	)

	writeMessages(t, writerConn, expected)
	if _, err := received.ReadFrom(readerConn); err != nil {
		t.Fatal(err)
	}

	for i, got := range received {
		want := expected[i]
		if !bytes.Equal(got, want) {
			t.Errorf("buffer %d did not match expected data"+
				"\n\tgot: %s"+
				"\n\twant: %s",
				i, got, want,
			)
		}
	}
}

func newLocalListener(t *testing.T) (*net.UDPConn, *net.UDPAddr) {
	t.Helper()
	addr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	addr.Port = conn.LocalAddr().(*net.UDPAddr).Port
	return conn, addr
}

func dialListener(t *testing.T, readerAddr *net.UDPAddr) *net.UDPConn {
	t.Helper()
	writerConn, err := net.DialUDP("udp", nil, readerAddr)
	if err != nil {
		t.Fatal(err)
	}
	return writerConn
}

func writeMessages(t *testing.T, conn *net.UDPConn, expected Buffers) {
	for _, buf := range expected {
		if _, _, err := conn.WriteMsgUDP(buf, nil, nil); err != nil {
			t.Fatal(err)
			return
		}
	}
}
