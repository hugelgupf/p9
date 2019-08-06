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
	"io"
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
