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

package composefs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hugelgupf/p9/fsimpl/localfs"
	"github.com/hugelgupf/p9/fsimpl/staticfs"
	"github.com/hugelgupf/p9/fsimpl/test"
	"github.com/hugelgupf/p9/p9"
)

func TestFilesMatch(t *testing.T) {
	localfsTmp := t.TempDir()
	setUmask()
	// Windows permissions are always represented as 0666 when writable.
	if err := os.WriteFile(filepath.Join(localfsTmp, "somefile"), []byte("hahaha"), 0666); err != nil {
		t.Fatal(err)
	}

	attacher, err := New(
		WithFile("foo.txt", staticfs.ReadOnlyFile("barbarbar")),
		WithFile("baz.txt", staticfs.ReadOnlyFile("barbarbarbar")),
		WithMount("localfs", localfs.Attacher(localfsTmp)),
	)
	if err != nil {
		t.Fatal(err)
	}

	test.TestReadOnlyFS(t, attacher,
		test.WithDir("", "foo.txt", "baz.txt", "localfs"),
		test.WithDir("localfs", "somefile"),
		test.WithFile("foo.txt", "barbarbar", p9.Attr{
			Mode:      p9.ModeRegular | 0666,
			Size:      9,
			BlockSize: 4096,
		}, p9.AttrMaskAll),
		test.WithFile("baz.txt", "barbarbarbar", p9.Attr{
			Mode:      p9.ModeRegular | 0666,
			Size:      12,
			BlockSize: 4096,
		}, p9.AttrMaskAll),
		test.WithFile("localfs/somefile", "hahaha", p9.Attr{
			Mode: p9.ModeRegular | 0666,
			Size: 6,
		}, p9.AttrMask{Mode: true, Size: true}),
	)
}
