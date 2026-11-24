//go:build !windows

package localfs

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/hugelgupf/p9/p9"
	"golang.org/x/sys/unix"
)

func isWindowsSymlinkPrivilegeErr(error) bool {
	return false
}

func TestOpenedFileGetAttrAndSetAttrAfterUnlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ephemeral")
	if err := os.WriteFile(path, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}

	root, err := Attacher(dir).Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	_, f, err := root.Walk([]string{"ephemeral"})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if _, _, err := f.Open(p9.ReadWrite); err != nil {
		t.Fatal(err)
	}
	if err := root.UnlinkAt("ephemeral", 0); err != nil {
		t.Fatal(err)
	}

	// POSIX fstat on an unlinked open file must still work.
	qid, _, attr, err := f.GetAttr(p9.AttrMaskAll)
	if err != nil {
		t.Fatalf("GetAttr on unlinked open file = %v", err)
	}
	if qid.Type != p9.TypeRegular || attr.Size != 11 {
		t.Errorf("GetAttr = (type=%v, size=%d), want (TypeRegular, 11)", qid.Type, attr.Size)
	}

	// ftruncate and fchmod on an unlinked open file must still work.
	if err := f.SetAttr(
		p9.SetAttrMask{Size: true, Permissions: true},
		p9.SetAttr{Size: 5, Permissions: 0600},
	); err != nil {
		t.Fatalf("SetAttr on unlinked open file = %v", err)
	}
	_, _, attr, err = f.GetAttr(p9.AttrMaskAll)
	if err != nil {
		t.Fatalf("GetAttr after SetAttr = %v", err)
	}
	if attr.Size != 5 || attr.Mode.Permissions() != 0600 {
		t.Errorf("after SetAttr: size=%d perm=%#o, want size=5 perm=0600", attr.Size, attr.Mode.Permissions())
	}

	// On Linux, updating timestamps on an unlinked open file works via
	// /proc/self/fd without needing the directory entry.
	if runtime.GOOS == "linux" {
		wantTime := time.Unix(4567, 0)
		if err := f.SetAttr(
			p9.SetAttrMask{ATime: true, ATimeNotSystemTime: true, MTime: true, MTimeNotSystemTime: true},
			p9.SetAttr{
				ATimeSeconds: uint64(wantTime.Unix()),
				MTimeSeconds: uint64(wantTime.Unix()),
			},
		); err != nil {
			t.Fatalf("SetAttr(ATime/MTime) on unlinked open file = %v", err)
		}
		_, _, attr, err = f.GetAttr(p9.AttrMaskAll)
		if err != nil {
			t.Fatalf("GetAttr after time SetAttr = %v", err)
		}
		if attr.ATimeSeconds != uint64(wantTime.Unix()) || attr.MTimeSeconds != uint64(wantTime.Unix()) {
			t.Errorf("unlinked times = (%d, %d), want (%d, %d)",
				attr.ATimeSeconds, attr.MTimeSeconds, wantTime.Unix(), wantTime.Unix())
		}
	}
}

func TestReadOnlyOpenSetAttrNoSplitBrain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target")
	if err := os.WriteFile(path, []byte("original-content"), 0644); err != nil {
		t.Fatal(err)
	}

	root, err := Attacher(dir).Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	_, f, err := root.Walk([]string{"target"})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, _, err := f.Open(p9.ReadOnly); err != nil {
		t.Fatal(err)
	}

	// Unlink and replace target with a different inode at the same name.
	if err := root.UnlinkAt("target", 0); err != nil {
		t.Fatal(err)
	}
	const replacement = "replacement-must-stay-intact"
	if err := os.WriteFile(path, []byte(replacement), 0644); err != nil {
		t.Fatal(err)
	}

	// Path-verification fallback and truncateAtNoFollow with want must
	// reject the mismatched replacement inode with ESTALE.
	var openSt unix.Stat_t
	if err := withFileFD(f.(*Local).file, func(fd int) error {
		return fstat(fd, &openSt)
	}); err != nil {
		t.Fatal(err)
	}
	dirFD, release, err := root.(*Local).openDir()
	if err != nil {
		t.Fatal(err)
	}
	err = verifyTargetMatchesFile(f.(*Local).file, dirFD, "target")
	truncErr := truncateAtNoFollow(dirFD, "target", 0, &openSt)
	release()
	if !errors.Is(err, unix.ESTALE) {
		t.Errorf("verifyTargetMatchesFile(replaced) = %v, want ESTALE", err)
	}
	if !errors.Is(truncErr, unix.ESTALE) {
		t.Errorf("truncateAtNoFollow(replaced, want=openSt) = %v, want ESTALE", truncErr)
	}

	// Truncating and updating mtime on the ReadOnly-opened fid must never
	// mutate the replacement file.
	wantTime := time.Unix(5678, 0)
	setErr := f.SetAttr(
		p9.SetAttrMask{Size: true, MTime: true, MTimeNotSystemTime: true},
		p9.SetAttr{Size: 4, MTimeSeconds: uint64(wantTime.Unix())},
	)
	if runtime.GOOS == "linux" {
		if setErr != nil {
			t.Fatalf("SetAttr on unlinked ReadOnly file = %v", setErr)
		}
		_, _, attr, err := f.GetAttr(p9.AttrMaskAll)
		if err != nil {
			t.Fatalf("GetAttr after ReadOnly SetAttr = %v", err)
		}
		if attr.Size != 4 || attr.MTimeSeconds != uint64(wantTime.Unix()) {
			t.Errorf("open inode after SetAttr = (size=%d, mtime=%d), want (4, %d)",
				attr.Size, attr.MTimeSeconds, wantTime.Unix())
		}
	} else if !errors.Is(setErr, unix.ESTALE) {
		t.Errorf("SetAttr on replaced ReadOnly file = %v, want ESTALE", setErr)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != replacement {
		t.Errorf("replacement file mutated: got %q, want %q", string(got), replacement)
	}
}

func TestFIFOOperations(t *testing.T) {
	dir := t.TempDir()
	fifoPath := filepath.Join(dir, "pipe")
	if err := unix.Mkfifo(fifoPath, 0644); err != nil {
		t.Fatalf("Mkfifo = %v", err)
	}

	root, err := Attacher(dir).Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	// Walk and GetAttr must recognize the FIFO without blocking.
	qids, f, err := root.Walk([]string{"pipe"})
	if err != nil {
		t.Fatalf("Walk(pipe) = %v", err)
	}
	defer f.Close()
	if len(qids) != 1 || qids[0].Type != p9.TypeAppendOnly {
		t.Fatalf("Walk(pipe) qids = %v, want TypeAppendOnly", qids)
	}
	_, _, attr, err := f.GetAttr(p9.AttrMaskAll)
	if err != nil {
		t.Fatalf("GetAttr(pipe) = %v", err)
	}
	if !attr.Mode.IsNamedPipe() {
		t.Fatalf("GetAttr(pipe) mode = %v, want named pipe", attr.Mode)
	}

	// SetAttr(Size) on a FIFO without a reader must fail promptly,
	// not block.
	done := make(chan error, 1)
	go func() {
		done <- f.SetAttr(p9.SetAttrMask{Size: true}, p9.SetAttr{Size: 0})
	}()
	select {
	case err := <-done:
		if !errors.Is(err, unix.EINVAL) {
			t.Errorf("SetAttr(Size) on FIFO = %v, want EINVAL", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SetAttr(Size) on FIFO blocked")
	}

	// Open with O_NONBLOCK must succeed without blocking.
	_, fNB, err := root.Walk([]string{"pipe"})
	if err != nil {
		t.Fatal(err)
	}
	defer fNB.Close()
	if _, _, err := fNB.Open(p9.ReadOnly | p9.OpenFlags(unix.O_NONBLOCK)); err != nil {
		t.Fatalf("Open(FIFO, O_RDONLY|O_NONBLOCK) = %v", err)
	}

	// Open(O_RDONLY) with a concurrent writer peer must succeed.
	_, fRD, err := root.Walk([]string{"pipe"})
	if err != nil {
		t.Fatal(err)
	}
	defer fRD.Close()
	_, fWR, err := root.Walk([]string{"pipe"})
	if err != nil {
		t.Fatal(err)
	}
	defer fWR.Close()

	wrDone := make(chan error, 1)
	go func() {
		_, _, err := fWR.Open(p9.WriteOnly)
		wrDone <- err
	}()
	if _, _, err := fRD.Open(p9.ReadOnly); err != nil {
		t.Fatalf("Open(FIFO, O_RDONLY) with writer = %v", err)
	}
	if err := <-wrDone; err != nil {
		t.Fatalf("Open(FIFO, O_WRONLY) with reader = %v", err)
	}
}

func TestUnlinkAtRemoveDirFlag(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "reg")
	subPath := filepath.Join(dir, "sub")
	if err := os.WriteFile(regPath, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(subPath, 0755); err != nil {
		t.Fatal(err)
	}

	root, err := Attacher(dir).Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	// AT_REMOVEDIR on a regular file must return ENOTDIR and keep the file.
	if err := root.UnlinkAt("reg", linuxATRemoveDir); !errors.Is(err, unix.ENOTDIR) {
		t.Errorf("UnlinkAt(reg, AT_REMOVEDIR) = %v, want ENOTDIR", err)
	}
	if _, err := os.Stat(regPath); err != nil {
		t.Errorf("regular file was deleted by AT_REMOVEDIR: %v", err)
	}

	// AT_REMOVEDIR on an empty directory must succeed.
	if err := root.UnlinkAt("sub", linuxATRemoveDir); err != nil {
		t.Errorf("UnlinkAt(sub, AT_REMOVEDIR) = %v", err)
	}
	if _, err := os.Stat(subPath); !os.IsNotExist(err) {
		t.Errorf("sub still exists after UnlinkAt(AT_REMOVEDIR): %v", err)
	}
}

func TestRootFDSharedAcrossFIDs(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "export")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	const numFiles = 100
	for i := range numFiles {
		name := "f" + strconv.Itoa(i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
	}

	root, err := Attacher(dir).Attach()
	if err != nil {
		t.Fatal(err)
	}
	rLocal := root.(*Local)

	children := make([]p9.File, 0, numFiles)
	for i := range numFiles {
		_, c, err := root.Walk([]string{"f" + strconv.Itoa(i)})
		if err != nil {
			t.Fatal(err)
		}
		if c.(*Local).root != rLocal.root {
			t.Fatalf("child %d does not share pinned rootDir", i)
		}
		children = append(children, c)
	}

	// Closing the root fid (including double Close) must not close the
	// shared root fd while child fids remain open.
	if err := root.Close(); err != nil {
		t.Fatalf("root.Close() = %v", err)
	}
	if err := root.Close(); err != nil {
		t.Fatalf("second root.Close() = %v", err)
	}
	if _, _, _, err := root.GetAttr(p9.AttrMaskAll); err != nil {
		t.Fatalf("root.GetAttr after Close = %v", err)
	}
	if _, _, _, err := children[0].GetAttr(p9.AttrMaskAll); err != nil {
		t.Fatalf("child.GetAttr after root.Close = %v", err)
	}

	// Concurrent Walk, GetAttr, and Close across goroutines must stay race
	// free and keep refcounts exact.
	var wg sync.WaitGroup
	for _, c := range children {
		wg.Add(1)
		go func(f p9.File) {
			defer wg.Done()
			if _, clone, err := f.Walk(nil); err == nil {
				_, _, _, _ = clone.GetAttr(p9.AttrMaskAll)
				_ = clone.Close()
			}
			if err := f.Close(); err != nil {
				t.Errorf("child.Close() = %v", err)
			}
		}(c)
	}
	wg.Wait()

	// Once all fids close, the root fd must be closed immediately (not
	// waiting on GC) so host mountpoints can be unmounted without EBUSY.
	state := rLocal.root.state
	state.mu.Lock()
	fdAfterClose, refsAfterClose := state.fd, state.refs
	state.mu.Unlock()
	if fdAfterClose != -1 || refsAfterClose != 0 {
		t.Fatalf("after closing all fids: state = (fd=%d, refs=%d), want (-1, 0)", fdAfterClose, refsAfterClose)
	}

	// Post-close ops (as with p9 Txattrwalk fid aliasing) lazily reopen
	// the verified root fd and close it again when the op finishes.
	if _, _, _, err := root.GetAttr(p9.AttrMaskAll); err != nil {
		t.Fatalf("root.GetAttr after all fids closed = %v", err)
	}
	state.mu.Lock()
	fdAfterReopen := state.fd
	state.mu.Unlock()
	if fdAfterReopen != -1 {
		t.Fatalf("root fd stayed open after post-close op: fd = %d, want -1", fdAfterReopen)
	}

	// If the export root directory is replaced while fd is closed, lazy
	// reopen must reject the different inode with ESTALE.
	if err := os.Rename(dir, filepath.Join(parent, "export_old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := root.GetAttr(p9.AttrMaskAll); !errors.Is(err, unix.ESTALE) {
		t.Errorf("root.GetAttr after root replacement = %v, want ESTALE", err)
	}
}
