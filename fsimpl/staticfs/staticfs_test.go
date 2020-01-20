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
	"io"
	"io/ioutil"
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

	root, err := attacher.Attach()
	if err != nil {
		t.Fatalf("Failed to attach to %v: %v", attacher, err)
	}

	// Make sure Readdir lists the files we want to find.
	if _, _, err := root.Open(p9.ReadOnly); err != nil {
		t.Fatalf("Open(%v) = %v, want nil", root, err)
	}
	dirents, err := root.Readdir(0, 2)
	if err != nil {
		t.Fatalf("Readdir(%v) = %v, want nil", root, err)
	}
	if dirents.Find("foo.txt") == nil {
		t.Errorf("Readdir(%v) = %v, does not contain foo.txt", root, dirents)
	}
	if dirents.Find("baz.txt") == nil {
		t.Errorf("Readdir(%v) = %v, does not contain baz.txt", root, dirents)
	}

	files := map[string]string{
		"foo.txt": "barbarbar",
		"baz.txt": "barbarbarbar",
	}
	for name, wantCon := range files {
		// Let's make sure the file content matches, at least twice.
		_, f, err := root.Walk([]string{name})
		if err != nil {
			t.Fatalf("Walk(%s) = %v, want nil", name, err)
		}
		if _, _, err := f.Open(p9.ReadOnly); err != nil {
			t.Fatalf("Open(%v) = %v, want nil", f, err)
		}

		for i := 0; i < 2; i++ {
			con, err := ioutil.ReadAll(io.NewSectionReader(f, 0, 30))
			if err != nil {
				t.Fatalf("%d ReadAll(%v) = %v, want nil", i, f, err)
			}
			if got := string(con); got != wantCon {
				t.Fatalf("%d ReadAll(%v) = %v, want %v", i, f, got, wantCon)
			}
		}

		if err := f.Close(); err != nil {
			t.Errorf("Close(%v) = %v, want nil", f, err)
		}
	}
}
