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
