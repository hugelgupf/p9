package localfs

import (
	"os"
	"path/filepath"

	"github.com/hugelgupf/p9/linux"
	"github.com/hugelgupf/p9/p9"
	"golang.org/x/sys/windows"
)

func umask(_ int) int {
	return 0
}

func localToQid(path string, info os.FileInfo) (uint64, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}

	var (
		access     uint32 // none; we only need metadata
		sharemode  uint32
		createmode uint32 = windows.OPEN_EXISTING
		attribute  uint32 = windows.FILE_ATTRIBUTE_NORMAL
	)
	if info.IsDir() {
		attribute = windows.FILE_FLAG_BACKUP_SEMANTICS
	}
	fd, err := windows.CreateFile(pathPtr, access, sharemode, nil, createmode, attribute, 0)
	if err != nil {
		return 0, err
	}

	fi := &windows.ByHandleFileInformation{}
	if err = windows.GetFileInformationByHandle(fd, fi); err != nil {
		return 0, err
	}

	x := uint64(fi.FileIndexHigh)<<32 | uint64(fi.FileIndexLow)
	return x, nil
}

// lock implements p9.File.Lock.
// As in FreeBSD NFS locking, we just say "sure, we did it" without actually
// doing anything; this lock design makes even less sense on Windows than
// it does on Linux (pid? really? what were they thinking?)
func (l *Local) lock(pid int, locktype p9.LockType, flags p9.LockFlags, start, length uint64, client string) (p9.LockStatus, error) {
	return p9.LockStatusOK, linux.ENOSYS
}

func statFSForPath(p string) (p9.FSStat, error) {
	lpDirectoryName, err := windows.UTF16PtrFromString(filepath.Dir(p))
	if err != nil {
		return p9.FSStat{}, err
	}
	var freeAvail, totalBytes, totalFree uint64
	if err = windows.GetDiskFreeSpaceEx(lpDirectoryName, &freeAvail, &totalBytes, &totalFree); err != nil {
		return p9.FSStat{}, err
	}
	// r1, _, e := windows.GetDiskFreeSpaceEx(lpDirectoryName, &freeAvail, &totalBytes, &totalFree)
	// if r1 == 0 {
	// 	if e != nil {
	// 		return p9.FSStat{}, e
	// 	}
	// 	return p9.FSStat{}, windows.ERROR_GEN_FAILURE
	// }

	// Derive a block size; Windows API doesn’t give it directly.
	// You can call GetDiskFreeSpace for sectors/cluster, but we’ll pick 4096 as a typical size.
	const blockSize = 4096

	return p9.FSStat{
		Type:            0, // not meaningful on Windows
		BlockSize:       blockSize,
		Blocks:          totalBytes / blockSize,
		BlocksFree:      totalFree / blockSize,
		BlocksAvailable: freeAvail / blockSize,
		Files:           0, // Windows doesn’t expose inode counts
		FilesFree:       0,
		FSID:            0, // optional; could hash volume serial number if desired
		// FIXME: fetch this. Long names might be enabled.
		NameLength: 255,
	}, nil
}
