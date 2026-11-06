//go:build linux || darwin || freebsd

package localfs

import (
	"github.com/hugelgupf/p9/p9"
	"golang.org/x/sys/unix"
)

// StatFS implements p9.File.StatFS.
//
// Report the numbers for the file system holding the exported directory. This
// is only built where unix.Statfs exists; elsewhere templatefs leaves StatFS
// unimplemented and a client sees a volume of unknown size.
func (l *Local) StatFS() (p9.FSStat, error) {
	var s unix.Statfs_t
	if err := unix.Statfs(l.path, &s); err != nil {
		return p9.FSStat{}, err
	}
	return p9.FSStat{
		// The unix.Statfs_t field types differ by platform, so every
		// field is converted rather than assigned.
		Type:            uint32(s.Type),
		BlockSize:       uint32(s.Bsize),
		Blocks:          uint64(s.Blocks),
		BlocksFree:      uint64(s.Bfree),
		BlocksAvailable: uint64(s.Bavail),
		Files:           uint64(s.Files),
		FilesFree:       uint64(s.Ffree),
		NameLength:      255,
	}, nil
}
