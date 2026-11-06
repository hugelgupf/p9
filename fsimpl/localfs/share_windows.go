package localfs

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// openRegular opens a regular file sharing read, write and delete access, so
// that, as on Unix, it can still be renamed over or deleted while open.
// os.OpenFile does not share delete access.
func openRegular(path string, flag int, perm os.FileMode) (*os.File, error) {
	if flag&os.O_APPEND != 0 {
		return os.OpenFile(path, flag, perm)
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}

	var access uint32
	switch flag & (os.O_RDONLY | os.O_WRONLY | os.O_RDWR) {
	case os.O_RDONLY:
		access = windows.GENERIC_READ
	case os.O_WRONLY:
		access = windows.GENERIC_WRITE
	case os.O_RDWR:
		access = windows.GENERIC_READ | windows.GENERIC_WRITE
	}

	var disposition uint32
	switch {
	case flag&(os.O_CREATE|os.O_EXCL) == os.O_CREATE|os.O_EXCL:
		disposition = windows.CREATE_NEW
	case flag&(os.O_CREATE|os.O_TRUNC) == os.O_CREATE|os.O_TRUNC:
		disposition = windows.CREATE_ALWAYS
	case flag&os.O_CREATE != 0:
		disposition = windows.OPEN_ALWAYS
	case flag&os.O_TRUNC != 0:
		disposition = windows.TRUNCATE_EXISTING
	default:
		disposition = windows.OPEN_EXISTING
	}

	attrs := uint32(windows.FILE_ATTRIBUTE_NORMAL)
	if flag&os.O_CREATE != 0 && perm&0200 == 0 {
		attrs = windows.FILE_ATTRIBUTE_READONLY
	}

	share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE)
	h, err := windows.CreateFile(p, access, share, nil, disposition, attrs, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

// fileRenameInfoEx is FILE_RENAME_INFO with the Flags member of Windows 10
// 1709 and later, used with the FileRenameInfoEx class.
type fileRenameInfoEx struct {
	Flags          uint32
	RootDirectory  windows.Handle
	FileNameLength uint32
	FileName       [1]uint16
}

// rename renames with POSIX semantics: the target is replaced even while it
// is open, and a directory with open files inside can move. It falls back to
// os.Rename where the file system does not support them.
func rename(oldPath, newPath string) error {
	src, err := windows.UTF16PtrFromString(oldPath)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: err}
	}
	dst, err := windows.UTF16FromString(newPath)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: err}
	}
	h, err := windows.CreateFile(src, windows.DELETE|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return os.Rename(oldPath, newPath)
	}
	defer windows.CloseHandle(h)

	nameLen := (len(dst) - 1) * 2 // in bytes, without the terminating NUL
	size := int(unsafe.Offsetof(fileRenameInfoEx{}.FileName)) + nameLen + 2
	if minSize := int(unsafe.Sizeof(fileRenameInfoEx{})); size < minSize {
		size = minSize
	}
	buf := make([]uint64, (size+7)/8) // aligned for RootDirectory
	info := (*fileRenameInfoEx)(unsafe.Pointer(&buf[0]))
	info.Flags = windows.FILE_RENAME_REPLACE_IF_EXISTS | windows.FILE_RENAME_POSIX_SEMANTICS | windows.FILE_RENAME_IGNORE_READONLY_ATTRIBUTE
	info.FileNameLength = uint32(nameLen)
	copy(unsafe.Slice(&info.FileName[0], len(dst)), dst)

	if err := windows.SetFileInformationByHandle(h, windows.FileRenameInfoEx, (*byte)(unsafe.Pointer(info)), uint32(size)); err != nil {
		return os.Rename(oldPath, newPath)
	}
	return nil
}
