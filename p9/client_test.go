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

	"github.com/hugelgupf/p9/internal/linux"
	"github.com/u-root/u-root/pkg/ulog/ulogtest"
	"google.golang.org/grpc/test/bufconn"
)

// TestVersion tests the version negotiation.
func TestVersion(t *testing.T) {
	// First, create a new server and connection.
	l := bufconn.Listen(int(DefaultMessageSize))

	// Create a new server and client.
	s := NewServer(nil, WithServerLogger(ulogtest.Logger{TB: t}))
	go s.Serve(l)

	client, err := l.Dial()
	if err != nil {
		t.Fatalf("got %v, expected nil", err)
	}

	// NewClient does a Tversion exchange, so this is our test for success.
	c, err := NewClient(client,
		WithMessageSize(1024*1024 /* 1M message size */),
		WithClientLogger(ulogtest.Logger{TB: t}),
	)
	if err != nil {
		t.Fatalf("got %v, expected nil", err)
	}

	// Check a bogus version string.
	if err := c.sendRecv(&tversion{Version: "notokay", MSize: 1024 * 1024}, &rversion{}); err != linux.EINVAL {
		t.Errorf("got %v expected %v", err, linux.EINVAL)
	}

	// Check a bogus version number.
	if err := c.sendRecv(&tversion{Version: "9P1000.L", MSize: 1024 * 1024}, &rversion{}); err != linux.EINVAL {
		t.Errorf("got %v expected %v", err, linux.EINVAL)
	}

	// Check a too high version number.
	if err := c.sendRecv(&tversion{Version: versionString(highestSupportedVersion + 1), MSize: 1024 * 1024}, &rversion{}); err != linux.EAGAIN {
		t.Errorf("got %v expected %v", err, linux.EAGAIN)
	}

	// Check an invalid MSize.
	if err := c.sendRecv(&tversion{Version: versionString(highestSupportedVersion), MSize: 0}, &rversion{}); err != linux.EINVAL {
		t.Errorf("got %v expected %v", err, linux.EINVAL)
	}
}
