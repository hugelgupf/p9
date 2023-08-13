package readdir

import (
	"github.com/hugelgupf/p9/p9"
)

func min(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

// Readdir can be used to implement p9.File.Readdir for static file systems.
//
// Repeated calls for the same directory must be made with the same names and
// types.
func Readdir(offset uint64, count uint32, names []string, qids map[string]p9.QID) (p9.Dirents, error) {
	// The value of offset can be implementation defined, as long as it
	// follows the doc:
	//
	// "On subsequent calls, offset is the offset returned
	// in the last directory entry of the previous call."
	//
	// We choose the entry index.
	if offset >= uint64(len(names)) {
		return nil, nil
	}

	var dirents []p9.Dirent

	// count is actually the number of _bytes_ requested by the client.
	//
	// p9.File.Readdir implementation can return way more entries, and p9
	// server will marshal the bytes and make it fit in `count` bytes.
	end := int(min(offset+uint64(count), uint64(len(names))))
	for i, name := range names[offset:end] {
		dirents = append(dirents, p9.Dirent{
			QID:  qids[name],
			Type: qids[name].Type,
			// "On subsequent calls, offset is the offset returned
			// in the last directory entry of the previous call."
			Offset: offset + uint64(i) + 1,
			Name:   name,
		})
	}
	return dirents, nil
}
