package internal

import (
	"os"
)

// NOTE: taken from amd64 Linux
type Timespec struct {
	Sec  int64
	Nsec int64
}

type Stat_t struct {
	Dev     uint64
	Ino     uint64
	Nlink   uint64
	Mode    uint32
	Uid     uint32
	Gid     uint32
	Rdev    uint64
	Size    int64
	Blksize int64
	Blocks  int64
	Atim    Timespec
	Mtim    Timespec
	Ctim    Timespec
}

// InfoToStat takes a platform native FileInfo and converts it into a 9P2000.L compatible Stat_t
func InfoToStat(fi os.FileInfo) *Stat_t {
	return &Stat_t{
		Size: fi.Size(),
		Mode: uint32(modeFromOS(fi.Mode())),
		Mtim: Timespec{
			Sec:  fi.ModTime().Unix(),
			Nsec: fi.ModTime().UnixNano(),
		},
	}

}

// TODO: copied from pkg p9
// we should probably migrate the OS methods from p9 into sys
const (
	FileModeMask        uint32 = 0170000
	ModeSocket                 = 0140000
	ModeSymlink                = 0120000
	ModeRegular                = 0100000
	ModeBlockDevice            = 060000
	ModeDirectory              = 040000
	ModeCharacterDevice        = 020000
	ModeNamedPipe              = 010000
)

// execBits are the execute permission bits for owner, group and others.
const execBits = 0111

// modeFromOS converts a Windows os.FileMode into 9P2000.L mode bits.
//
// Windows has no execute permission bit, and Go reports every regular file
// as 0666 (or 0444 when read-only). Go already adds the execute bits to
// directories, so we do the same for regular files. Otherwise a 9P client
// can never run a binary or script from the share, and chmod cannot help
// because Windows has nowhere to store the bit. This matches what the WSL
// 9P server reports for Windows files by default.
func modeFromOS(mode os.FileMode) uint32 {
	m := uint32(mode.Perm())
	switch {
	case mode.IsDir():
		m |= ModeDirectory
	case mode&os.ModeSymlink != 0:
		m |= ModeSymlink
	case mode&os.ModeSocket != 0:
		m |= ModeSocket
	case mode&os.ModeNamedPipe != 0:
		m |= ModeNamedPipe
	case mode&os.ModeCharDevice != 0:
		m |= ModeCharacterDevice
	case mode&os.ModeDevice != 0:
		m |= ModeBlockDevice
	default:
		m |= ModeRegular | execBits
	}
	return m
}
