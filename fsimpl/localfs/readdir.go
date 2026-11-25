package localfs

import (
	"hash/fnv"
	"os"
	"path"

	"github.com/hugelgupf/p9/p9"
)

// Readdir implements p9.File.Readdir.
func (l *Local) Readdir(offset uint64, count uint32) (p9.Dirents, error) {
	fi, err := os.ReadDir(l.path)
	// os.ReadDir returns the entries it read before failing; serve those
	// rather than nothing.
	if err != nil && len(fi) == 0 {
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
			// The entry exists but can't be stat'ed (e.g. no search
			// permission on l.path). List it anyway, like ls does;
			// the client gets the real error when it walks to it.
			qid = p9.QID{
				Type: p9.ModeFromOS(fi[i].Type()).QIDType(),
				Path: fallbackQidPath(entry.path),
			}
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

// fallbackQidPath returns a stable qid path for an entry that couldn't be
// stat'ed. It is never 0: clients turn qid.path into d_ino, and readdir(3)
// skips entries whose d_ino is 0.
func fallbackQidPath(p string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(p))
	if q := h.Sum64(); q != 0 {
		return q
	}
	return 1
}
