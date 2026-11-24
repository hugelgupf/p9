package localfs

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hugelgupf/p9/linux"
	"github.com/hugelgupf/p9/p9"
)

func TestRejectInvalidNames(t *testing.T) {
	parent := t.TempDir()
	export := filepath.Join(parent, "export")
	if err := os.MkdirAll(filepath.Join(export, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "secret"), []byte("outside"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(export, "legit"), []byte("inside"), 0644); err != nil {
		t.Fatal(err)
	}

	root, err := Attacher(export).Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	_, sub, err := root.Walk([]string{"sub"})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	_, legit, err := root.Walk([]string{"legit"})
	if err != nil {
		t.Fatal(err)
	}
	defer legit.Close()

	badNames := []string{
		"",
		".",
		"..",
		"../secret",
		"a/b",
		"/etc/passwd",
		"bad\x00name",
	}
	if runtime.GOOS == "windows" {
		badNames = append(badNames,
			`..\secret`,
			`a\b`,
			`\secret`,
			`C:secret`,
			`NUL`,
			`. `,
			`.. `,
			`...`,
			`   `,
			`foo.`,
			`foo `,
		)
	}

	for _, name := range badNames {
		if _, _, err := root.Walk([]string{name}); !errors.Is(err, linux.EINVAL) {
			t.Errorf("root.Walk(%q) = %v, want EINVAL", name, err)
		}
		if _, _, err := sub.Walk([]string{name}); !errors.Is(err, linux.EINVAL) {
			t.Errorf("sub.Walk(%q) = %v, want EINVAL", name, err)
		}
		if _, _, _, err := root.Create(name, p9.ReadWrite, 0644, p9.NoUID, p9.NoGID); !errors.Is(err, linux.EINVAL) {
			t.Errorf("root.Create(%q) = %v, want EINVAL", name, err)
		}
		if _, err := root.Mkdir(name, 0755, p9.NoUID, p9.NoGID); !errors.Is(err, linux.EINVAL) {
			t.Errorf("root.Mkdir(%q) = %v, want EINVAL", name, err)
		}
		if _, err := root.Symlink("target", name, p9.NoUID, p9.NoGID); !errors.Is(err, linux.EINVAL) {
			t.Errorf("root.Symlink(%q) = %v, want EINVAL", name, err)
		}
		if err := root.Link(legit, name); !errors.Is(err, linux.EINVAL) {
			t.Errorf("root.Link(%q) = %v, want EINVAL", name, err)
		}
		if err := root.RenameAt(name, root, "dst"); !errors.Is(err, linux.EINVAL) {
			t.Errorf("root.RenameAt(old=%q) = %v, want EINVAL", name, err)
		}
		if err := root.RenameAt("legit", root, name); !errors.Is(err, linux.EINVAL) {
			t.Errorf("root.RenameAt(new=%q) = %v, want EINVAL", name, err)
		}
		if err := root.UnlinkAt(name, 0); !errors.Is(err, linux.EINVAL) {
			t.Errorf("root.UnlinkAt(%q) = %v, want EINVAL", name, err)
		}
	}

	// Multi-component walk containing ".." must fail too.
	if _, _, err := root.Walk([]string{"sub", "..", "..", "secret"}); !errors.Is(err, linux.EINVAL) {
		t.Errorf("root.Walk(sub/../..) = %v, want EINVAL", err)
	}

	// Renamed with an unsafe name must fail closed on subsequent ops.
	_, victim, err := root.Walk([]string{"legit"})
	if err != nil {
		t.Fatal(err)
	}
	defer victim.Close()
	victim.Renamed(root, "../secret")
	if _, _, _, err := victim.GetAttr(p9.AttrMaskAll); !errors.Is(err, linux.EINVAL) {
		t.Errorf("GetAttr after invalid Renamed = %v, want EINVAL", err)
	}
	if _, _, err := victim.Open(p9.ReadOnly); !errors.Is(err, linux.EINVAL) {
		t.Errorf("Open after invalid Renamed = %v, want EINVAL", err)
	}
}
