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

func localToQid(path string, info os.FileInfo, f *os.File) (uint64, error) {
	var handle windows.Handle
	if f != nil {
		handle = windows.Handle(f.Fd())
	} else {
		name, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return 0, err
		}
		const (
			access = 0 // none; we only need metadata.
			mode   = windows.FILE_SHARE_READ |
				windows.FILE_SHARE_WRITE |
				windows.FILE_SHARE_DELETE
			createmode   = windows.OPEN_EXISTING
			templatefile = 0
		)
		var attrs uint32 = windows.FILE_ATTRIBUTE_NORMAL
		if info.IsDir() {
			attrs = windows.FILE_FLAG_BACKUP_SEMANTICS
		}
		handle, err = windows.CreateFile(
			name, access, mode,
			nil, createmode, attrs,
			templatefile)
		if err != nil {
			return 0, err
		}
		defer windows.CloseHandle(handle) // FIXME: dropped error; join it.
	}
	var fiByHandle windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &fiByHandle); err != nil {
		return 0, err
	}
	fileIndex := uint64(fiByHandle.FileIndexHigh)<<32 |
		uint64(fiByHandle.FileIndexLow)
	return fileIndex, nil
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
