package localfs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hugelgupf/p9/p9"
)

// TestGetAttrExecutable checks that regular files on Windows are reported
// with execute bits, so 9P clients can run binaries and scripts from a share.
func TestGetAttrExecutable(t *testing.T) {
	dir := t.TempDir()
	for _, tt := range []struct {
		name     string
		readOnly bool
		want     p9.FileMode
	}{
		{name: "rw", want: 0777},
		{name: "ro", readOnly: true, want: 0555},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := filepath.Join(dir, tt.name+".sh")
			if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0666); err != nil {
				t.Fatal(err)
			}
			if tt.readOnly {
				if err := os.Chmod(p, 0444); err != nil {
					t.Fatal(err)
				}
				// Let t.TempDir clean up the file.
				t.Cleanup(func() { os.Chmod(p, 0666) })
			}

			root, err := Attacher(dir).Attach()
			if err != nil {
				t.Fatal(err)
			}
			_, f, err := root.Walk([]string{tt.name + ".sh"})
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()

			_, _, attr, err := f.GetAttr(p9.AttrMask{Mode: true})
			if err != nil {
				t.Fatal(err)
			}
			if !attr.Mode.IsRegular() {
				t.Fatalf("GetAttr mode = %v, want regular file", attr.Mode)
			}
			if got := attr.Mode.Permissions(); got != tt.want {
				t.Errorf("GetAttr permissions = %#o, want %#o", got, tt.want)
			}
		})
	}
}

// TestBackslashNames checks that names using Windows path separators cannot
// reach outside the exported directory.
func TestBackslashNames(t *testing.T) {
	dir := t.TempDir()
	share := filepath.Join(dir, "share")
	if err := os.Mkdir(share, 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secret"), nil, 0666); err != nil {
		t.Fatal(err)
	}

	root, err := Attacher(share).Attach()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := root.Walk([]string{`..\secret`}); err == nil {
		t.Errorf("Walk(%q) = nil, want error", `..\secret`)
	}
	if _, _, _, err := root.Create(`..\created`, p9.WriteOnly, 0666, 0, 0); err == nil {
		t.Errorf("Create(%q) = nil, want error", `..\created`)
	}
	if _, err := root.Mkdir(`..\dir`, 0777, 0, 0); err == nil {
		t.Errorf("Mkdir(%q) = nil, want error", `..\dir`)
	}
	if err := root.UnlinkAt(`..\secret`, 0); err == nil {
		t.Errorf("UnlinkAt(%q) = nil, want error", `..\secret`)
	}
	for _, name := range []string{"created", "dir"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("%s was created outside the exported directory", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "secret")); err != nil {
		t.Errorf("secret outside the exported directory: %v", err)
	}
}
