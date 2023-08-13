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

package staticfs

import (
	"testing"

	"github.com/hugelgupf/p9/fsimpl/test"
	"github.com/hugelgupf/p9/p9"
)

func TestReadOnlyFS(t *testing.T) {
	attacher, err := New(WithFile("foo", "barbarbar"))
	if err != nil {
		t.Fatal(err)
	}
	test.TestReadOnlyFS(t, attacher)
}

func TestFilesMatch(t *testing.T) {
	attacher, err := New(
		WithFile("foo.txt", "barbarbar"),
		WithFile("baz.txt", "barbarbarbar"),
	)
	if err != nil {
		t.Fatal(err)
	}

	test.TestReadOnlyFS(t, attacher,
		test.WithFile("foo.txt", "barbarbar", p9.Attr{
			Mode:      p9.ModeRegular | 0666,
			Size:      9,
			BlockSize: 4096,
		}),
		test.WithFile("baz.txt", "barbarbarbar", p9.Attr{
			Mode:      p9.ModeRegular | 0666,
			Size:      12,
			BlockSize: 4096,
		}),
		test.WithDir("", "foo.txt", "baz.txt"),
	)
}
