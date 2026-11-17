//go:build unix

package localfs

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hugelgupf/p9/p9"
	"golang.org/x/sys/unix"
)

func TestEncodeLikely(t *testing.T) {
	var maxMajorMinorBits = 12
	if runtime.GOOS == "darwin" {
		maxMajorMinorBits = 8
	}

	tests := []struct {
		name       string
		dev        uint64
		ino        uint64
		wantOk     bool
		wantResult uint64 // Only checked if wantOk is true
	}{
		{
			name:       "Simple Valid Case",
			dev:        unix.Mkdev(0, 0),
			ino:        123,
			wantOk:     true,
			wantResult: 123 | (0 << 39) | (0 << 51),
		},
		{
			name:       "Various different numbers",
			dev:        unix.Mkdev(123, 456),
			ino:        789,
			wantOk:     true,
			wantResult: 789 | (456 << 39) | (123 << 51),
		},
		{
			name:       "Max Likely Inode",
			dev:        0,
			ino:        (uint64(1) << 39) - 1,
			wantOk:     true,
			wantResult: (uint64(1) << 39) - 1,
		},
		{
			name:       "Max Likely Major Minor",
			dev:        unix.Mkdev(1<<maxMajorMinorBits-1, 1<<maxMajorMinorBits-1),
			ino:        123,
			wantOk:     true,
			wantResult: 123 | (1<<maxMajorMinorBits-1)<<39 | (1<<maxMajorMinorBits-1)<<51,
		},
		{
			name:   "Inode Too Large",
			dev:    0,
			ino:    uint64(1) << 39,
			wantOk: false,
		},
		{
			name:   "Minor Too Large",
			dev:    unix.Mkdev(0, (1 << 12)),
			ino:    1,
			wantOk: false,
		},
		{
			name:   "Major Too Large",
			dev:    unix.Mkdev((1 << 12), 0),
			ino:    1,
			wantOk: false,
		},
		{
			name:   "Upper Bits Set in Dev",
			dev:    uint64(1) << 32,
			ino:    1,
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := encodeLikely(tt.dev, tt.ino)
			if ok != tt.wantOk {
				t.Errorf("encodeLikely() ok = %v, want %v", ok, tt.wantOk)
				return
			}
			if ok && got != tt.wantResult {
				t.Errorf("encodeLikely() got = %d, want %d", got, tt.wantResult)
			}
		})
	}
}

func TestLock(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "lockfile")
	if err := os.WriteFile(path, []byte("lockdata"), 0666); err != nil {
		t.Fatal(err)
	}

	root, err := Attacher(tempDir).Attach()
	if err != nil {
		t.Fatal(err)
	}
	_, f, err := root.Walk([]string{"lockfile"})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// Locking before Open returns EBADF.
	if status, err := f.Lock(os.Getpid(), p9.WriteLock, 0, 0, 4, "test"); err == nil || status != p9.LockStatusError {
		t.Fatalf("Lock before Open = (%v, %v), want (LockStatusError, EBADF)", status, err)
	}

	if _, _, err := f.Open(p9.ReadWrite); err != nil {
		t.Fatal(err)
	}
	if status, err := f.Lock(os.Getpid(), p9.ReadLock, p9.LockFlagsBlock, 0, 4, "test"); err != nil || status != p9.LockStatusOK {
		t.Fatalf("Lock(ReadLock, Block) = (%v, %v), want (LockStatusOK, nil)", status, err)
	}
	if status, err := f.Lock(os.Getpid(), p9.WriteLock, 0, 0, 4, "test"); err != nil || status != p9.LockStatusOK {
		t.Fatalf("Lock(WriteLock) = (%v, %v), want (LockStatusOK, nil)", status, err)
	}
	if status, err := f.Lock(os.Getpid(), p9.Unlock, 0, 0, 4, "test"); err != nil || status != p9.LockStatusOK {
		t.Fatalf("Lock(Unlock) = (%v, %v), want (LockStatusOK, nil)", status, err)
	}
	if status, err := f.Lock(os.Getpid(), p9.WriteLock, p9.LockFlagsReclaim, 0, 4, "test"); err == nil || status != p9.LockStatusError {
		t.Fatalf("Lock(Reclaim) = (%v, %v), want error", status, err)
	}
	if status, err := f.Lock(os.Getpid(), p9.LockType(99), 0, 0, 4, "test"); err == nil || status != p9.LockStatusError {
		t.Fatalf("Lock(invalid type) = (%v, %v), want error", status, err)
	}
}

func TestReaddirUnixSpecialTypes(t *testing.T) {
	// Prefer /tmp directly so the Unix socket path stays well below 104
	// bytes on macOS and BSDs even when $TMPDIR is long.
	shortDir, err := os.MkdirTemp("/tmp", "p9dt-")
	if err != nil {
		shortDir, err = os.MkdirTemp("", "p9dt-")
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortDir) })

	if err := unix.Mkfifo(filepath.Join(shortDir, "fifo"), 0600); err != nil {
		t.Fatalf("Mkfifo: %v", err)
	}
	ln, err := net.Listen("unix", filepath.Join(shortDir, "sock"))
	if err != nil {
		t.Fatalf("Listen(unix): %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	root := serveLocalFS(t, shortDir)
	if _, _, err := root.Open(p9.ReadOnly); err != nil {
		t.Fatal(err)
	}
	ents, err := root.Readdir(0, 8192)
	if err != nil {
		t.Fatalf("Readdir = %v", err)
	}
	want := map[string]p9.QIDType{
		"fifo": dtFIFO,
		"sock": dtSock,
	}
	if len(ents) != len(want) {
		t.Fatalf("got %d entries (%v), want %d", len(ents), ents, len(want))
	}
	for _, e := range ents {
		if got := e.Type; got != want[e.Name] {
			t.Errorf("entry %q: Dirent.Type = %#x, want %#x", e.Name, uint8(got), uint8(want[e.Name]))
		}
	}
}
