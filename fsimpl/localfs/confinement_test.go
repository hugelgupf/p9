package localfs

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/hugelgupf/p9/fsimpl/templatefs"
	"github.com/hugelgupf/p9/linux"
	"github.com/hugelgupf/p9/p9"
)

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		if isWindowsSymlinkPrivilegeErr(err) {
			t.Skipf("symlinks require elevated privileges on this Windows host: %v", err)
		}
		t.Fatalf("os.Symlink(%q, %q) = %v", target, link, err)
	}
}

func TestAttachValidation(t *testing.T) {
	tmp := t.TempDir()

	if _, err := Attacher(filepath.Join(tmp, "does-not-exist")).Attach(); err == nil {
		t.Error("Attach on non-existent directory succeeded, want error")
	}

	reg := filepath.Join(tmp, "regular")
	if err := os.WriteFile(reg, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Attacher(reg).Attach(); err == nil {
		t.Error("Attach on regular file succeeded, want error")
	}
}

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
	if _, _, err := victim.Walk(nil); !errors.Is(err, linux.EINVAL) {
		t.Errorf("Walk(nil) after invalid Renamed = %v, want EINVAL", err)
	}
	if _, _, _, err := victim.GetAttr(p9.AttrMaskAll); !errors.Is(err, linux.EINVAL) {
		t.Errorf("GetAttr after invalid Renamed = %v, want EINVAL", err)
	}
	if _, _, err := victim.Open(p9.ReadOnly); !errors.Is(err, linux.EINVAL) {
		t.Errorf("Open after invalid Renamed = %v, want EINVAL", err)
	}
}

func TestCrossRootEXDEV(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	if err := os.WriteFile(filepath.Join(dirA, "a"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}

	rootA, err := Attacher(dirA).Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer rootA.Close()

	rootB, err := Attacher(dirB).Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer rootB.Close()

	_, fileA, err := rootA.Walk([]string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	defer fileA.Close()

	if err := rootB.Link(fileA, "linked"); !errors.Is(err, linux.EXDEV) {
		t.Errorf("cross-root Link = %v, want EXDEV", err)
	}
	if err := rootA.RenameAt("a", rootB, "moved"); !errors.Is(err, linux.EXDEV) {
		t.Errorf("cross-root RenameAt = %v, want EXDEV", err)
	}

	// Cross-root Renamed must fail closed on subsequent ops.
	_, victim, err := rootA.Walk([]string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	defer victim.Close()
	victim.Renamed(rootB, "a")
	if _, _, _, err := victim.GetAttr(p9.AttrMaskAll); !errors.Is(err, linux.EINVAL) {
		t.Errorf("GetAttr after cross-root Renamed = %v, want EINVAL", err)
	}

	// Nil, typed-nil, and foreign p9.File arguments must not panic.
	var foreign p9.File = &templatefs.NoopFile{}
	var typedNil *Local
	if err := rootA.Link(nil, "x"); !errors.Is(err, linux.EBADF) {
		t.Errorf("Link(nil) = %v, want EBADF", err)
	}
	if err := rootA.Link(typedNil, "x"); !errors.Is(err, linux.EBADF) {
		t.Errorf("Link((*Local)(nil)) = %v, want EBADF", err)
	}
	if err := rootA.Link(foreign, "x"); !errors.Is(err, linux.EXDEV) {
		t.Errorf("Link(foreign) = %v, want EXDEV", err)
	}
	if err := rootA.RenameAt("a", nil, "x"); !errors.Is(err, linux.EBADF) {
		t.Errorf("RenameAt(nil) = %v, want EBADF", err)
	}
	if err := rootA.RenameAt("a", typedNil, "x"); !errors.Is(err, linux.EBADF) {
		t.Errorf("RenameAt((*Local)(nil)) = %v, want EBADF", err)
	}
	if err := rootA.RenameAt("a", foreign, "x"); !errors.Is(err, linux.EXDEV) {
		t.Errorf("RenameAt(foreign) = %v, want EXDEV", err)
	}
	for _, parent := range []p9.File{nil, typedNil, foreign} {
		_, f, err := rootA.Walk([]string{"a"})
		if err != nil {
			t.Fatal(err)
		}
		f.Renamed(parent, "a")
		if _, _, _, err := f.GetAttr(p9.AttrMaskAll); !errors.Is(err, linux.EINVAL) {
			t.Errorf("GetAttr after Renamed(%T) = %v, want EINVAL", parent, err)
		}
		f.Close()
	}

	// Two separate attaches of the same directory share the same root.
	rootA2, err := Attacher(dirA).Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer rootA2.Close()
	if err := rootA2.Link(fileA, "a2"); err != nil {
		t.Errorf("same-root Link across attaches = %v, want nil", err)
	}
}

func TestWalkRejectsIntermediateSymlinks(t *testing.T) {
	parent := t.TempDir()
	outside := filepath.Join(parent, "outside")
	export := filepath.Join(parent, "export")
	sibling := filepath.Join(export, "sibling")
	sub := filepath.Join(export, "sub")

	for _, d := range []string{outside, sibling, sub} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("outside"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sibling, "inner"), []byte("inside"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(export, "reg"), []byte("reg"), 0644); err != nil {
		t.Fatal(err)
	}

	mustSymlink(t, outside, filepath.Join(export, "sym_out"))
	mustSymlink(t, sibling, filepath.Join(export, "sym_in"))
	mustSymlink(t, filepath.Join("..", "sibling"), filepath.Join(sub, "sym_rel"))

	root, err := Attacher(export).Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	// Multi-component walks through symlinks (outside OR inside root)
	// must fail.
	for _, names := range [][]string{
		{"sym_out", "secret"},
		{"sym_in", "inner"},
		{"sub", "sym_rel", "inner"},
		{"reg", "child"},
	} {
		if _, _, err := root.Walk(names); err == nil {
			t.Errorf("Walk(%v) succeeded, want error", names)
		}
	}

	// Single-component walk to a symlink succeeds with TypeSymlink, but
	// walking further from the symlink fid must fail.
	qids, symFid, err := root.Walk([]string{"sym_out"})
	if err != nil {
		t.Fatalf("Walk([sym_out]) = %v", err)
	}
	defer symFid.Close()
	if len(qids) != 1 || qids[0].Type != p9.TypeSymlink {
		t.Fatalf("Walk([sym_out]) qids = %v, want TypeSymlink", qids)
	}
	if _, _, err := symFid.Walk([]string{"secret"}); err == nil {
		t.Error("symFid.Walk([secret]) succeeded, want error")
	}

	// Walking further from a regular file fid must fail.
	_, regFid, err := root.Walk([]string{"reg"})
	if err != nil {
		t.Fatal(err)
	}
	defer regFid.Close()
	if _, _, err := regFid.Walk([]string{"child"}); err == nil {
		t.Error("regFid.Walk([child]) succeeded, want error")
	}
}

func TestLeafSymlinkNoFollow(t *testing.T) {
	parent := t.TempDir()
	outside := filepath.Join(parent, "outside")
	export := filepath.Join(parent, "export")
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(export, 0755); err != nil {
		t.Fatal(err)
	}

	outsideTarget := filepath.Join(outside, "target")
	insideTarget := filepath.Join(export, "target")
	danglingTarget := filepath.Join(outside, "dangling_new")
	wantContent := "do-not-touch"
	for _, p := range []string{outsideTarget, insideTarget} {
		if err := os.WriteFile(p, []byte(wantContent), 0644); err != nil {
			t.Fatal(err)
		}
	}

	mustSymlink(t, outsideTarget, filepath.Join(export, "sym_out"))
	mustSymlink(t, "target", filepath.Join(export, "sym_in"))
	mustSymlink(t, danglingTarget, filepath.Join(export, "sym_dangling"))

	root, err := Attacher(export).Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	cases := []struct {
		name       string
		linkName   string
		targetPath string
	}{
		{name: "outside", linkName: "sym_out", targetPath: outsideTarget},
		{name: "inside", linkName: "sym_in", targetPath: insideTarget},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			beforeInfo, err := os.Stat(tc.targetPath)
			if err != nil {
				t.Fatal(err)
			}

			// Open on a leaf symlink must fail for every mode.
			for _, flags := range []p9.OpenFlags{
				p9.ReadOnly,
				p9.WriteOnly,
				p9.ReadWrite,
				p9.WriteOnly | p9.OpenFlags(os.O_TRUNC),
			} {
				_, sym, err := root.Walk([]string{tc.linkName})
				if err != nil {
					t.Fatal(err)
				}
				_, _, openErr := sym.Open(flags)
				sym.Close()
				if openErr == nil {
					t.Errorf("Open(%v) on %s succeeded, want error", flags, tc.linkName)
				}
			}

			// Create over an existing symlink must fail.
			if f, _, _, err := root.Create(tc.linkName, p9.ReadWrite, 0644, p9.NoUID, p9.NoGID); err == nil {
				f.Close()
				t.Errorf("Create(%s) over symlink succeeded, want error", tc.linkName)
			}

			// SetAttr(Size) must fail and leave target size intact.
			_, sym, err := root.Walk([]string{tc.linkName})
			if err != nil {
				t.Fatal(err)
			}
			defer sym.Close()
			if err := sym.SetAttr(p9.SetAttrMask{Size: true}, p9.SetAttr{Size: 0}); err == nil {
				t.Errorf("SetAttr(Size) on %s succeeded, want error", tc.linkName)
			}

			// SetAttr(Permissions) must not alter target mode.
			permErr := sym.SetAttr(p9.SetAttrMask{Permissions: true}, p9.SetAttr{Permissions: 0600})
			if runtime.GOOS == "linux" && permErr == nil {
				t.Errorf("SetAttr(Permissions) on %s succeeded on Linux, want error", tc.linkName)
			}

			// SetAttr(MTime) must not alter target mtime.
			oldTime := time.Unix(1000, 0)
			_ = sym.SetAttr(
				p9.SetAttrMask{MTime: true, MTimeNotSystemTime: true},
				p9.SetAttr{MTimeSeconds: uint64(oldTime.Unix())},
			)

			// Link must never follow the symlink to hard-link
			// target. On Unix/Windows linkat(..., 0) either
			// hard-links the symlink itself or fails; it must
			// never produce a regular file.
			hardLinkName := "hard_" + tc.name
			if err := root.Link(sym, hardLinkName); err == nil {
				li, statErr := os.Lstat(filepath.Join(export, hardLinkName))
				if statErr != nil {
					t.Fatalf("Lstat(%s) = %v", hardLinkName, statErr)
				}
				if li.Mode()&os.ModeSymlink == 0 {
					t.Errorf("Link(%s) followed symlink: new entry mode = %v, want symlink", tc.linkName, li.Mode())
				}
			}

			// Verify target content, mode, and mtime are intact.
			got, err := os.ReadFile(tc.targetPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != wantContent {
				t.Errorf("target content = %q, want %q", string(got), wantContent)
			}
			afterInfo, err := os.Stat(tc.targetPath)
			if err != nil {
				t.Fatal(err)
			}
			if afterInfo.Mode() != beforeInfo.Mode() {
				t.Errorf("target mode changed: got %v, want %v", afterInfo.Mode(), beforeInfo.Mode())
			}
			if !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
				t.Errorf("target mtime changed: got %v, want %v", afterInfo.ModTime(), beforeInfo.ModTime())
			}
		})
	}

	// Create over a dangling symlink must fail and leave target absent.
	if f, _, _, err := root.Create("sym_dangling", p9.ReadWrite, 0644, p9.NoUID, p9.NoGID); err == nil {
		f.Close()
		t.Error("Create over dangling symlink succeeded, want error")
	}
	if _, err := os.Lstat(danglingTarget); !os.IsNotExist(err) {
		t.Errorf("dangling symlink target was created: Lstat = %v", err)
	}
}

func TestStaleFIDIntermediateSymlinkReplacement(t *testing.T) {
	for _, insideRoot := range []bool{false, true} {
		name := "outside"
		if insideRoot {
			name = "inside_root"
		}
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			export := filepath.Join(parent, "export")
			var victimDir string
			var linkTarget string
			if insideRoot {
				victimDir = filepath.Join(export, "victim")
				linkTarget = "victim"
			} else {
				victimDir = filepath.Join(parent, "victim")
				linkTarget = victimDir
			}

			// Victim tree that would be exposed if d were followed.
			if err := os.MkdirAll(filepath.Join(victimDir, "nested"), 0755); err != nil {
				t.Fatal(err)
			}
			victimFile := filepath.Join(victimDir, "file")
			victimNested := filepath.Join(victimDir, "nested", "deep")
			if err := os.WriteFile(victimFile, []byte("victim-secret"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(victimNested, []byte("nested-secret"), 0644); err != nil {
				t.Fatal(err)
			}
			mustSymlink(t, "victim-link-target", filepath.Join(victimDir, "sym"))

			// Legitimate directory d inside export; walk fids in.
			dPath := filepath.Join(export, "d")
			if err := os.MkdirAll(filepath.Join(dPath, "nested"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dPath, "file"), []byte("orig"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dPath, "nested", "deep"), []byte("orig-deep"), 0644); err != nil {
				t.Fatal(err)
			}
			mustSymlink(t, "orig-link", filepath.Join(dPath, "sym"))
			if err := os.WriteFile(filepath.Join(export, "legit"), []byte("legit"), 0644); err != nil {
				t.Fatal(err)
			}

			root, err := Attacher(export).Attach()
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()

			_, staleDir, err := root.Walk([]string{"d"})
			if err != nil {
				t.Fatal(err)
			}
			defer staleDir.Close()

			_, staleFile, err := root.Walk([]string{"d", "file"})
			if err != nil {
				t.Fatal(err)
			}
			defer staleFile.Close()

			_, staleSym, err := root.Walk([]string{"d", "sym"})
			if err != nil {
				t.Fatal(err)
			}
			defer staleSym.Close()

			_, staleNested, err := root.Walk([]string{"d", "nested", "deep"})
			if err != nil {
				t.Fatal(err)
			}
			defer staleNested.Close()

			_, legit, err := root.Walk([]string{"legit"})
			if err != nil {
				t.Fatal(err)
			}
			defer legit.Close()

			// Replace directory d with a symlink to victimDir.
			if err := os.RemoveAll(dPath); err != nil {
				t.Fatal(err)
			}
			mustSymlink(t, linkTarget, dPath)

			// Every operation via stale fids must fail safely.
			if _, _, err := staleFile.Open(p9.ReadOnly); err == nil {
				t.Error("staleFile.Open(ReadOnly) succeeded, want error")
			}
			if _, _, err := staleFile.Open(p9.ReadWrite); err == nil {
				t.Error("staleFile.Open(ReadWrite) succeeded, want error")
			}
			if _, _, _, err := staleFile.GetAttr(p9.AttrMaskAll); err == nil {
				t.Error("staleFile.GetAttr succeeded, want error")
			}
			if err := staleFile.SetAttr(p9.SetAttrMask{Size: true}, p9.SetAttr{Size: 0}); err == nil {
				t.Error("staleFile.SetAttr(Size) succeeded, want error")
			}
			if err := staleFile.SetAttr(p9.SetAttrMask{Permissions: true}, p9.SetAttr{Permissions: 0600}); err == nil {
				t.Error("staleFile.SetAttr(Permissions) succeeded, want error")
			}
			if err := staleFile.SetAttr(p9.SetAttrMask{MTime: true, MTimeNotSystemTime: true}, p9.SetAttr{MTimeSeconds: 1000}); err == nil {
				t.Error("staleFile.SetAttr(MTime) succeeded, want error")
			}
			if err := staleFile.(*Local).Remove(); err == nil {
				t.Error("staleFile.Remove succeeded, want error")
			}
			if target, err := staleSym.Readlink(); err == nil {
				t.Errorf("staleSym.Readlink() = %q, want error", target)
			}
			if _, _, err := staleNested.Open(p9.ReadOnly); err == nil {
				t.Error("staleNested.Open succeeded, want error")
			}
			if _, _, _, err := staleNested.GetAttr(p9.AttrMaskAll); err == nil {
				t.Error("staleNested.GetAttr succeeded, want error")
			}
			if err := root.Link(staleFile, "stolen"); err == nil {
				t.Error("root.Link(staleFile) succeeded, want error")
			}
			if _, _, err := staleDir.Walk([]string{"file"}); err == nil {
				t.Error("staleDir.Walk([file]) succeeded, want error")
			}
			if f, _, _, err := staleDir.Create("pwned", p9.ReadWrite, 0644, p9.NoUID, p9.NoGID); err == nil {
				f.Close()
				t.Error("staleDir.Create succeeded, want error")
			}
			if _, err := staleDir.Mkdir("pwned_dir", 0755, p9.NoUID, p9.NoGID); err == nil {
				t.Error("staleDir.Mkdir succeeded, want error")
			}
			if _, err := staleDir.Symlink("x", "pwned_sym", p9.NoUID, p9.NoGID); err == nil {
				t.Error("staleDir.Symlink succeeded, want error")
			}
			if err := staleDir.Link(legit, "pwned_link"); err == nil {
				t.Error("staleDir.Link succeeded, want error")
			}
			if err := staleDir.RenameAt("file", root, "stolen_rename"); err == nil {
				t.Error("staleDir.RenameAt(from staleDir) succeeded, want error")
			}
			if err := root.RenameAt("legit", staleDir, "file"); err == nil {
				t.Error("root.RenameAt(to staleDir) succeeded, want error")
			}
			if err := staleDir.UnlinkAt("file", 0); err == nil {
				t.Error("staleDir.UnlinkAt succeeded, want error")
			}
			if _, err := staleDir.Readdir(0, 100); err == nil {
				t.Error("staleDir.Readdir succeeded, want error")
			}

			// Verify victimDir was completely untouched.
			got, err := os.ReadFile(victimFile)
			if err != nil {
				t.Fatalf("victimFile missing or unreadable: %v", err)
			}
			if string(got) != "victim-secret" {
				t.Errorf("victimFile = %q, want %q", string(got), "victim-secret")
			}
			for _, unwanted := range []string{"pwned", "pwned_dir", "pwned_sym", "pwned_link"} {
				if _, err := os.Lstat(filepath.Join(victimDir, unwanted)); !os.IsNotExist(err) {
					t.Errorf("unexpected entry %s created in victimDir: %v", unwanted, err)
				}
			}
		})
	}
}

func TestSymlinkNormalOperations(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(targetPath, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	root, err := Attacher(dir).Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	// Create both a relative symlink and a dangling absolute symlink.
	if _, err := root.Symlink("real.txt", "rel_link", p9.NoUID, p9.NoGID); err != nil {
		if isWindowsSymlinkPrivilegeErr(err) {
			t.Skipf("symlinks require elevated privileges on this Windows host: %v", err)
		}
		t.Fatalf("Symlink(rel_link) = %v", err)
	}
	if _, err := root.Symlink("/nonexistent/absolute/target", "abs_link", p9.NoUID, p9.NoGID); err != nil {
		t.Fatalf("Symlink(abs_link) = %v", err)
	}

	for _, tc := range []struct {
		name   string
		target string
	}{
		{"rel_link", "real.txt"},
		{"abs_link", "/nonexistent/absolute/target"},
	} {
		qids, f, err := root.Walk([]string{tc.name})
		if err != nil {
			t.Fatalf("Walk(%s) = %v", tc.name, err)
		}
		if len(qids) != 1 || qids[0].Type != p9.TypeSymlink {
			t.Errorf("Walk(%s) qids = %v, want TypeSymlink", tc.name, qids)
		}
		qid, _, attr, err := f.GetAttr(p9.AttrMaskAll)
		if err != nil {
			t.Fatalf("GetAttr(%s) = %v", tc.name, err)
		}
		if qid.Type != p9.TypeSymlink || !attr.Mode.IsSymlink() {
			t.Errorf("GetAttr(%s) = (qid=%v, mode=%v), want symlink", tc.name, qid, attr.Mode)
		}
		got, err := f.Readlink()
		if err != nil {
			t.Fatalf("Readlink(%s) = %v", tc.name, err)
		}
		// Go 1.26 preserves '/' in Windows os.Root.Symlink/Readlink,
		// while Go 1.27 normalizes to '\'; accept either separator.
		if filepath.ToSlash(got) != tc.target {
			t.Errorf("Readlink(%s) = %q, want %q", tc.name, got, tc.target)
		}
		f.Close()
	}

	// Readdir must report TypeSymlink for the symlinks, not TypeRegular.
	_, dirCopy, err := root.Walk(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dirCopy.Close()
	if _, _, err := dirCopy.Open(p9.ReadOnly); err != nil {
		t.Fatal(err)
	}
	ents, err := dirCopy.Readdir(0, 10)
	if err != nil {
		t.Fatalf("Readdir = %v", err)
	}
	for _, name := range []string{"rel_link", "abs_link"} {
		e := ents.Find(name)
		if e == nil {
			t.Fatalf("Readdir missing %s", name)
		}
		if e.Type != p9.TypeSymlink || e.QID.Type != p9.TypeSymlink {
			t.Errorf("Readdir(%s) type = (%v, %v), want TypeSymlink", name, e.Type, e.QID.Type)
		}
	}

	// Unlinking the symlink removes only the link, keeping real.txt.
	if err := root.UnlinkAt("rel_link", 0); err != nil {
		t.Fatalf("UnlinkAt(rel_link) = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "rel_link")); !os.IsNotExist(err) {
		t.Errorf("rel_link still exists after UnlinkAt: %v", err)
	}
	if got, err := os.ReadFile(targetPath); err != nil || string(got) != "hello" {
		t.Errorf("real.txt after UnlinkAt(rel_link) = (%q, %v), want (hello, nil)", string(got), err)
	}

	// Remove on symlink fid removes only the symlink.
	_, absFid, err := root.Walk([]string{"abs_link"})
	if err != nil {
		t.Fatal(err)
	}
	defer absFid.Close()
	if err := absFid.(*Local).Remove(); err != nil {
		t.Fatalf("Remove(abs_link) = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "abs_link")); !os.IsNotExist(err) {
		t.Errorf("abs_link still exists after Remove: %v", err)
	}
}

func TestSetAttrOnRootAndRenamedTracking(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "d1"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "d1", "f"), []byte("payload"), 0644); err != nil {
		t.Fatal(err)
	}

	root, err := Attacher(dir).Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	// SetAttr(MTime) on export root itself must work; Size must fail.
	mtime := time.Unix(2000, 0)
	if err := root.SetAttr(
		p9.SetAttrMask{MTime: true, MTimeNotSystemTime: true},
		p9.SetAttr{MTimeSeconds: uint64(mtime.Unix())},
	); err != nil {
		t.Fatalf("root.SetAttr(MTime) = %v", err)
	}
	if err := root.SetAttr(p9.SetAttrMask{Size: true}, p9.SetAttr{Size: 0}); err == nil {
		t.Error("root.SetAttr(Size) succeeded, want error")
	}

	// Rename d1 -> d2 and update fids via Renamed.
	_, d1, err := root.Walk([]string{"d1"})
	if err != nil {
		t.Fatal(err)
	}
	defer d1.Close()
	_, f, err := d1.Walk([]string{"f"})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if err := root.RenameAt("d1", root, "d2"); err != nil {
		t.Fatalf("RenameAt(d1 -> d2) = %v", err)
	}
	d1.Renamed(root, "d2")
	f.Renamed(d1, "f")

	if _, _, err := f.Open(p9.ReadOnly); err != nil {
		t.Fatalf("Open after Renamed = %v", err)
	}
	buf := make([]byte, 16)
	n, err := f.ReadAt(buf, 0)
	if n != len("payload") || string(buf[:n]) != "payload" {
		t.Fatalf("ReadAt after Renamed = (%q, %v), want (payload, nil/EOF)", string(buf[:n]), err)
	}
}

func TestNestedDirectoryOperations(t *testing.T) {
	dir := t.TempDir()
	root, err := Attacher(dir).Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	// Exercise Create and Mkdir with non-0777 mode bits (e.g. sticky/type
	// bits) to verify masking works on Windows os.Root too.
	if _, err := root.Mkdir("a", p9.Sticky|0755, p9.NoUID, p9.NoGID); err != nil {
		t.Fatalf("Mkdir(a) = %v", err)
	}
	_, dirA, err := root.Walk([]string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	defer dirA.Close()

	if _, err := dirA.Mkdir("b", 0755, p9.NoUID, p9.NoGID); err != nil {
		t.Fatalf("Mkdir(a/b) = %v", err)
	}
	_, dirB, err := root.Walk([]string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	defer dirB.Close()

	f, _, _, err := dirB.Create("file", p9.ReadWrite, p9.ModeRegular|0644, p9.NoUID, p9.NoGID)
	if err != nil {
		t.Fatalf("Create(a/b/file) = %v", err)
	}
	if _, err := f.WriteAt([]byte("nested-data"), 0); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	// Exercise GetAttr, SetAttr (all fields), Link, Symlink, Readlink,
	// Readdir, RenameAt, and UnlinkAt inside a nested directory (O_PATH
	// parent fd on Linux).
	_, nf, err := root.Walk([]string{"a", "b", "file"})
	if err != nil {
		t.Fatal(err)
	}
	defer nf.Close()

	_, _, attr, err := nf.GetAttr(p9.AttrMaskAll)
	if err != nil {
		t.Fatalf("GetAttr(a/b/file) = %v", err)
	}
	if attr.Size != 11 {
		t.Fatalf("size = %d, want 11", attr.Size)
	}

	mtime := time.Unix(3000, 0)
	if err := nf.SetAttr(
		p9.SetAttrMask{Size: true, MTime: true, MTimeNotSystemTime: true, Permissions: true},
		p9.SetAttr{Size: 6, MTimeSeconds: uint64(mtime.Unix()), Permissions: 0600},
	); err != nil {
		t.Fatalf("SetAttr(a/b/file) = %v", err)
	}
	if runtime.GOOS != "windows" {
		if err := nf.SetAttr(p9.SetAttrMask{UID: true, GID: true}, p9.SetAttr{UID: attr.UID, GID: attr.GID}); err != nil {
			t.Fatalf("SetAttr(UID/GID) = %v", err)
		}
	}

	if err := dirB.Link(nf, "hard"); err != nil {
		t.Fatalf("Link(a/b/hard) = %v", err)
	}
	if err := dirB.RenameAt("hard", dirA, "moved"); err != nil {
		t.Fatalf("RenameAt(a/b/hard -> a/moved) = %v", err)
	}
	if err := dirA.UnlinkAt("moved", 0); err != nil {
		t.Fatalf("UnlinkAt(a/moved) = %v", err)
	}

	if _, err := dirB.Symlink("file", "link", p9.NoUID, p9.NoGID); err == nil {
		_, sym, err := dirB.Walk([]string{"link"})
		if err != nil {
			t.Fatalf("Walk(a/b/link) = %v", err)
		}
		gotTarget, err := sym.Readlink()
		sym.Close()
		if err != nil || gotTarget != "file" {
			t.Errorf("Readlink(a/b/link) = (%q, %v), want (file, nil)", gotTarget, err)
		}
		if err := dirB.UnlinkAt("link", 0); err != nil {
			t.Errorf("UnlinkAt(a/b/link) = %v", err)
		}
	} else if !isWindowsSymlinkPrivilegeErr(err) {
		t.Fatalf("Symlink(a/b/link) = %v", err)
	}

	_, dirBOpen, err := dirB.Walk(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dirBOpen.Close()
	if _, _, err := dirBOpen.Open(p9.ReadOnly); err != nil {
		t.Fatal(err)
	}
	ents, err := dirBOpen.Readdir(0, 10)
	if err != nil {
		t.Fatalf("Readdir(a/b) = %v", err)
	}
	if ents.Find("file") == nil {
		t.Errorf("Readdir(a/b) = %v, missing file", ents)
	}
}

func serveAttacher(t *testing.T, a p9.Attacher) p9.File {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	srv := p9.NewServer(a)
	srvDone := make(chan error, 1)
	go func() {
		srvDone <- srv.Handle(serverConn, serverConn)
	}()

	client, err := p9.NewClient(clientConn)
	if err != nil {
		serverConn.Close()
		clientConn.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		client.Close()
		serverConn.Close()
		<-srvDone
	})

	root, err := client.Attach("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root
}

func serveLocalFS(t *testing.T, export string) p9.File {
	t.Helper()
	return serveAttacher(t, Attacher(export))
}

func TestServerClientConfinementEndToEnd(t *testing.T) {
	parent := t.TempDir()
	outside := filepath.Join(parent, "outside")
	export := filepath.Join(parent, "export")
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(export, 0755); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outside, "secret")
	if err := os.WriteFile(outsideFile, []byte("top-secret"), 0644); err != nil {
		t.Fatal(err)
	}
	mustSymlink(t, outsideFile, filepath.Join(export, "sym_secret"))

	root := serveLocalFS(t, export)

	// Exercise Txattrwalk on root (whose server-side clunk aliases and
	// closes the root Local) and confirm the mount remains healthy for
	// subsequent GetAttr and Walk operations.
	_, _ = root.ListXattrs()
	_, _ = root.GetXattr("user.nonexistent")
	if _, _, _, err := root.GetAttr(p9.AttrMaskAll); err != nil {
		t.Fatalf("client root.GetAttr after xattrwalk = %v", err)
	}

	// Walking to the symlink and reading its target works.
	_, sym, err := root.Walk([]string{"sym_secret"})
	if err != nil {
		t.Fatalf("client Walk(sym_secret) = %v", err)
	}
	defer sym.Close()
	if got, err := sym.Readlink(); err != nil || got != outsideFile {
		t.Fatalf("client Readlink(sym_secret) = (%q, %v), want (%q, nil)", got, err, outsideFile)
	}
	// Opening the symlink over 9P must fail.
	if _, _, err := sym.Open(p9.ReadOnly); err == nil {
		t.Error("client Open(sym_secret) succeeded, want error")
	}

	// Create, open, unlink, and GetAttr on the open fid works on Unix.
	if runtime.GOOS != "windows" {
		_, dirForCreate, err := root.Walk(nil)
		if err != nil {
			t.Fatal(err)
		}
		defer dirForCreate.Close()
		f, _, _, err := dirForCreate.Create("ephemeral", p9.ReadWrite, 0644, p9.NoUID, p9.NoGID)
		if err != nil {
			t.Fatalf("client Create = %v", err)
		}
		defer f.Close()
		if _, err := f.WriteAt([]byte("alive"), 0); err != nil {
			t.Fatalf("client WriteAt = %v", err)
		}
		if err := root.UnlinkAt("ephemeral", 0); err != nil {
			t.Fatalf("client UnlinkAt = %v", err)
		}
		_, _, attr, err := f.GetAttr(p9.AttrMaskAll)
		if err != nil {
			t.Fatalf("client GetAttr on unlinked open file = %v", err)
		}
		if attr.Size != 5 {
			t.Errorf("unlinked open file size = %d, want 5", attr.Size)
		}
	}
}

func TestSetAttrSizeOnAppendFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "append.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}

	root, err := Attacher(dir).Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	_, f, err := root.Walk([]string{"append.txt"})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if _, _, err := f.Open(p9.WriteOnly | p9.OpenFlags(os.O_APPEND)); err != nil {
		t.Fatalf("Open(O_APPEND) = %v", err)
	}
	if err := f.SetAttr(p9.SetAttrMask{Size: true}, p9.SetAttr{Size: 5}); err != nil {
		t.Fatalf("SetAttr(Size) on O_APPEND fid = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Errorf("file content = %q, want %q", string(got), "hello")
	}
}

type blockingReadAttacher struct {
	base     p9.Attacher
	readHook func()
}

func (a blockingReadAttacher) Attach() (p9.File, error) {
	f, err := a.base.Attach()
	if err != nil {
		return nil, err
	}
	return &blockingReadLocal{Local: f.(*Local), readHook: a.readHook}, nil
}

type blockingReadLocal struct {
	*Local
	readHook func()
}

func (b *blockingReadLocal) Walk(names []string) ([]p9.QID, p9.File, error) {
	qids, child, err := b.Local.Walk(names)
	if err != nil {
		return nil, nil, err
	}
	return qids, &blockingReadLocal{Local: child.(*Local), readHook: b.readHook}, nil
}

func (b *blockingReadLocal) ReadAt(p []byte, offset int64) (int, error) {
	b.readHook()
	return b.Local.ReadAt(p, offset)
}

func (b *blockingReadLocal) RenameAt(oldName string, newDir p9.File, newName string) error {
	if d, ok := newDir.(*blockingReadLocal); ok {
		newDir = d.Local
	}
	return b.Local.RenameAt(oldName, newDir, newName)
}

// TestServerLocalFSBlockingReadAtDoesNotStallRename checks end-to-end that
// Local's UnlockedIO opt-in allows a blocked ReadAt (e.g. a hung remote
// mount) to proceed concurrently with RenameAt without deadlocking renameMu.
func TestServerLocalFSBlockingReadAtDoesNotStallRename(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("world"), 0644); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	defer releaseOnce()
	root := serveAttacher(t, blockingReadAttacher{
		base: Attacher(dir),
		readHook: func() {
			close(entered)
			<-release
		},
	})

	_, f, err := root.Walk([]string{"f"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	if _, _, err := f.Open(p9.ReadOnly); err != nil {
		t.Fatalf("Open(f) = %v", err)
	}

	buf := make([]byte, 5)
	readDone := make(chan error, 1)
	go func() {
		_, err := f.ReadAt(buf, 0)
		readDone <- err
	}()
	<-entered

	renameDone := make(chan error, 1)
	go func() {
		renameDone <- root.RenameAt("a", root, "b")
	}()
	select {
	case err := <-renameDone:
		if err != nil {
			t.Fatalf("RenameAt while ReadAt blocked = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RenameAt deadlocked behind blocked localfs ReadAt")
	}

	releaseOnce()
	if err := <-readDone; err != nil {
		t.Fatalf("ReadAt(f) = %v", err)
	}
	if string(buf) != "hello" {
		t.Errorf("ReadAt(f) = %q, want %q", string(buf), "hello")
	}
}

func TestPrepareOpenDoubleFinishAndDirTrunc(t *testing.T) {
	dir := t.TempDir()
	reg := filepath.Join(dir, "reg")
	if err := os.WriteFile(reg, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}

	root, err := Attacher(dir).Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	_, f, err := root.Walk([]string{"reg"})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	finish, err := f.(*Local).PrepareOpen(p9.WriteOnly | p9.OpenFlags(os.O_TRUNC))
	if err != nil {
		t.Fatalf("PrepareOpen = %v", err)
	}
	if _, _, err := finish(); err != nil {
		t.Fatalf("first finish = %v", err)
	}
	if _, _, err := finish(); !errors.Is(err, linux.EINVAL) {
		t.Errorf("second finish = %v, want EINVAL", err)
	}
	got, err := os.ReadFile(reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("after O_TRUNC content = %q, want empty", string(got))
	}

	_, sub, err := root.Walk([]string{"sub"})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	if _, _, err := sub.Open(p9.ReadOnly | p9.OpenFlags(os.O_TRUNC)); linux.ExtractErrno(err) != linux.EISDIR {
		t.Errorf("Open(dir, ReadOnly|O_TRUNC) = %v, want EISDIR", err)
	}
}
