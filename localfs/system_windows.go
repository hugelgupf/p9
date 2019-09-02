package localfs

import (
	"os"

	"golang.org/x/sys/windows"
)

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
