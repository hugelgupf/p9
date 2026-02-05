package localfs

import (
	"testing"
)

func TestEncodeLikely(t *testing.T) {
	tests := []struct {
		name       string
		dev        uint64
		ino        uint64
		wantOk     bool
		wantResult uint64 // Only checked if wantOk is true
	}{
		{
			name:       "Simple Valid Case",
			dev:        mkDev(0, 0),
			ino:        123,
			wantOk:     true,
			wantResult: 123 | (0 << 39) | (0 << 51),
		},
		{
			name:       "Various different numbers",
			dev:        mkDev(123, 456),
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
			dev:        mkDev(1<<12-1, 1<<12-1),
			ino:        123,
			wantOk:     true,
			wantResult: 123 | (1<<12-1)<<39 | (1<<12-1)<<51,
		},
		{
			name:   "Inode Too Large",
			dev:    0,
			ino:    uint64(1) << 39,
			wantOk: false,
		},
		{
			name:   "Minor Too Large",
			dev:    mkDev(0, (1 << 12)),
			ino:    1,
			wantOk: false,
		},
		{
			name:   "Major Too Large",
			dev:    mkDev((1 << 12), 0),
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

func mkDev(major, minor uint32) uint64 {
	return (uint64(major) & 0x00000fff << 8) |
		(uint64(major) & 0xfffff000 << 32) |
		(uint64(minor) & 0x000000ff << 0) |
		(uint64(minor) & 0xffffff00 << 12)
}
