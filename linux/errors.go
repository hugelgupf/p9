package linux

import (
	"log"
	"os"
)

// ExtractErrno extracts an [Errno] from an error, best effort.
//
// If the system-specific or Go-specific error cannot be mapped to anything, it
// will be logged and EIO will be returned.
func ExtractErrno(err error) Errno {
	switch err {
	case os.ErrNotExist:
		return ENOENT
	case os.ErrExist:
		return EEXIST
	case os.ErrPermission:
		return EACCES
	case os.ErrInvalid:
		return EINVAL
	}

	// Attempt to unwrap.
	switch e := err.(type) {
	case Errno:
		return e
	case *os.PathError:
		return ExtractErrno(e.Err)
	case *os.SyscallError:
		return ExtractErrno(e.Err)
	case *os.LinkError:
		return ExtractErrno(e.Err)
	}

	if e := sysErrno(err); e != 0 {
		return e
	}

	// Default case.
	//
	// TODO: give the ability to turn this off.
	log.Printf("unknown error: %v", err)
	return EIO
}
