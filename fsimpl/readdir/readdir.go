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
	if offset >= uint64(len(names)) {
		return nil, nil
	}

	var dirents []p9.Dirent
	end := int(min(offset+uint64(count), uint64(len(names))))
	for i, name := range names[offset:end] {
		dirents = append(dirents, p9.Dirent{
			QID:    qids[name],
			Type:   qids[name].Type,
			Offset: offset + uint64(i),
			Name:   name,
		})
	}
	return dirents, nil
}
