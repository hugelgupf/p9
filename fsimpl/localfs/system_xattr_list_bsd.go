//go:build freebsd || netbsd

package localfs

import "golang.org/x/sys/unix"

// listXattrsFD decodes FreeBSD/NetBSD extattr_list_fd's 1-byte-length-prefixed
// entries per namespace and restores the "user." / "system." prefixes expected
// by 9P2000.L and x/sys/unix.{Fgetxattr,Fsetxattr,Fremovexattr}.
func listXattrsFD(fd int) ([]string, error) {
	var attrs []string
	for _, ns := range [...]struct {
		id     int
		prefix string
	}{
		{unix.EXTATTR_NAMESPACE_USER, "user."},
		{unix.EXTATTR_NAMESPACE_SYSTEM, "system."},
	} {
		raw, err := readXattrBytes(func(dest []byte) (int, error) {
			return unix.FlistxattrNS(fd, ns.id, dest)
		})
		if err != nil {
			if err == unix.EPERM && ns.id != unix.EXTATTR_NAMESPACE_USER {
				continue
			}
			return nil, err
		}
		for i := 0; i < len(raw); {
			n := int(raw[i])
			i++
			if i+n > len(raw) {
				break
			}
			if n > 0 {
				attrs = append(attrs, ns.prefix+string(raw[i:i+n]))
			}
			i += n
		}
	}
	return attrs, nil
}
