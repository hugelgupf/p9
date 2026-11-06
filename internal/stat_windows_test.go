package internal

import (
	"os"
	"testing"
)

func TestModeFromOS(t *testing.T) {
	for _, tt := range []struct {
		name string
		mode os.FileMode
		want uint32
	}{
		{
			name: "regular file",
			mode: 0666,
			want: ModeRegular | 0777,
		},
		{
			name: "read-only regular file",
			mode: 0444,
			want: ModeRegular | 0555,
		},
		{
			name: "directory",
			mode: os.ModeDir | 0777,
			want: ModeDirectory | 0777,
		},
		{
			name: "symlink",
			mode: os.ModeSymlink | 0666,
			want: ModeSymlink | 0666,
		},
		{
			name: "named pipe",
			mode: os.ModeNamedPipe | 0666,
			want: ModeNamedPipe | 0666,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := modeFromOS(tt.mode); got != tt.want {
				t.Errorf("modeFromOS(%v) = %#o, want %#o", tt.mode, got, tt.want)
			}
		})
	}
}
