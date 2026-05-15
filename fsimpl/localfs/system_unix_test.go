//go:build unix

package localfs

import (
	"runtime"
	"testing"

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
