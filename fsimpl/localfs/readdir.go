package localfs

import (
	"os"
	"path"

	"github.com/hugelgupf/p9/p9"
)

// Readdir implements p9.File.Readdir.
func (l *Local) Readdir(offset uint64, count uint32) (p9.Dirents, error) {
	fi, err := os.ReadDir(l.path)
	if err != nil {
		return nil, err
	}
	if offset >= uint64(len(fi)) {
		return nil, nil
	}
	var dirents p9.Dirents
	for i := int(offset); i < len(fi) && uint64(len(dirents)) < uint64(count); i++ {
		entry := Local{path: path.Join(l.path, fi[i].Name())}
		qid, _, err := entry.info()
		if err != nil {
			continue
		}
		dirents = append(dirents, p9.Dirent{
			QID:    qid,
			Type:   qid.Type,
			Name:   fi[i].Name(),
			Offset: uint64(i + 1),
		})
	}
	return dirents, nil
}
