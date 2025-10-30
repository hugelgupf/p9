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
	t := Timespec{Sec: int64(fi.ModTime().Second())}
	return &Stat_t{
		Dev:     0,
		Ino:     0,
		Nlink:   1,
		Mode:    uint32(fi.Mode()),
		Uid:     0,
		Gid:     0,
		Rdev:    0,
		Size:    fi.Size(),
		Blksize: 8192,
		Blocks:  fi.Size() / 8192,
		Atim:    t,
		Mtim:    t,
		Ctim:    t,
	}
}
