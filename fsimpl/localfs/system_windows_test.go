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

package localfs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hugelgupf/p9/p9"
)

// A lookup must not leave the file open: NTFS refuses to replace a file, or
// rename a directory holding one, while a handle without FILE_SHARE_DELETE
// is open on it.
func TestLookupDoesNotHoldFiles(t *testing.T) {
	dir := t.TempDir()
	root, err := Attacher(dir).Attach()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"c1", "c2", "src/f"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if _, _, err := root.Walk([]string{"c1"}); err != nil {
		t.Fatal(err)
	}
	if err := root.RenameAt("c2", root, "c1"); err != nil {
		t.Errorf("rename over a looked-up file: %v", err)
	}

	if _, _, err := root.Walk([]string{"src", "f"}); err != nil {
		t.Fatal(err)
	}
	if err := root.RenameAt("src", root, "dst"); err != nil {
		t.Errorf("rename a directory with a looked-up child: %v", err)
	}
}

// Files a client holds open must not block replacing or deleting them, which a
// Linux client expects to succeed as it does on its own file systems.
func TestOpenFilesCanBeReplacedAndDeleted(t *testing.T) {
	dir := t.TempDir()
	root, err := Attacher(dir).Attach()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"target", "victim"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("old"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	open := func(names ...string) p9.File {
		t.Helper()
		_, f, err := root.Walk(names)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := f.Open(p9.ReadOnly); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		return f
	}

	// Write a temporary file and rename it over a file another fid holds
	// open, both still open, as composer and editors do.
	target := open("target")
	_, d, err := root.Walk(nil)
	if err != nil {
		t.Fatal(err)
	}
	tmp, _, _, err := d.Create("target.tmp", p9.WriteOnly, 0644, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer tmp.Close()
	if _, err := tmp.WriteAt([]byte("new"), 0); err != nil {
		t.Fatal(err)
	}
	if err := root.RenameAt("target.tmp", root, "target"); err != nil {
		t.Fatalf("rename over an open file: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "target")); string(got) != "new" {
		t.Errorf("target content = %q, want %q", got, "new")
	}
	buf := make([]byte, 3)
	if _, err := target.ReadAt(buf, 0); err != nil || string(buf) != "old" {
		t.Errorf("the replaced file read through its open fid = %q, %v, want %q", buf, err, "old")
	}

	open("victim")
	if err := os.Remove(filepath.Join(dir, "victim")); err != nil {
		t.Errorf("host delete of a file a client holds open: %v", err)
	}
}
