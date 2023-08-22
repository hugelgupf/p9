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
	"sync"
	"testing"

	"github.com/hugelgupf/socketpair"
	"github.com/u-root/uio/ulog/ulogtest"
)

// TestVersion tests the version negotiation.
func TestVersion(t *testing.T) {
	// First, create a new server and connection.
	l := socketpair.Listen()

	// Create a new server and client.
	s := NewServer(nil, WithServerLogger(ulogtest.Logger{TB: t}))
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.Serve(l)
	}()
	defer wg.Wait()
	defer l.Close()

	client, err := l.Dial()
	if err != nil {
		t.Fatalf("got %v, expected nil", err)
	}
	defer client.Close()

	// NewClient does a Tversion exchange, so this is our test for success.
	c, err := NewClient(client,
		WithMessageSize(1024*1024 /* 1M message size */),
		WithClientLogger(ulogtest.Logger{TB: t}),
	)
	if err != nil {
		t.Fatalf("got %v, expected nil", err)
	}

	want := rversion{
		Version: "unknown",
		MSize:   0,
	}
	// Check a bogus version string.
	var r rversion
	if err := c.sendRecv(&tversion{Version: "notokay", MSize: 1024 * 1024}, &r); err != nil {
		t.Errorf("err %v", err)
	}
	if r != want {
		t.Errorf("got %v, want %v", r, want)
	}

	// Check a bogus version number.
	if err := c.sendRecv(&tversion{Version: "9P1000.L", MSize: 1024 * 1024}, &r); err != nil {
		t.Errorf("err %v", err)
	}
	if r != want {
		t.Errorf("got %v, want %v", r, want)
	}

	// Check an invalid MSize.
	if err := c.sendRecv(&tversion{Version: versionString(version9P2000L, highestSupportedVersion), MSize: 0}, &r); err != nil {
		t.Errorf("err %v", err)
	}
	if r != want {
		t.Errorf("got %v, want %v", r, want)
	}

	want = rversion{
		Version: versionString(version9P2000L, highestSupportedVersion),
		MSize:   1024 * 1024,
	}
	// Check a too high version number.
	if err := c.sendRecv(&tversion{Version: versionString(version9P2000L, highestSupportedVersion+1), MSize: 1024 * 1024}, &r); err != nil {
		t.Errorf("err %v", err)
	}
	if r != want {
		t.Errorf("got %v, want %v", r, want)
	}

}
