package localfs

import (
	"os"
	"path"

	"github.com/hugelgupf/p9/p9"
)

// direntHeaderSize is the fixed 9P2000.L wire size of a Dirent before its
// name bytes: qid[13] + offset[8] + type[1] + name_len[2] = 24 bytes.
const direntHeaderSize = 13 + 8 + 1 + 2

func direntType(mode os.FileMode) p9.QIDType {
	if mode&os.ModeIrregular != 0 {
		return 0
	}
	// 9P2000.L Rreaddir carries the Linux getdents d_type byte, i.e. POSIX
	// IFTODT(st_mode) = (st_mode & S_IFMT) >> 12.
	return p9.QIDType(p9.ModeFromOS(mode).FileType() >> 12)
}

// Readdir implements p9.File.Readdir.
func (l *Local) Readdir(offset uint64, count uint32) (p9.Dirents, error) {
	l.readdirMu.Lock()
	if offset == 0 || l.readdirEntries == nil {
		entries, err := os.ReadDir(l.path)
		if err != nil {
			l.readdirMu.Unlock()
			return nil, err
		}
		l.readdirEntries = entries
	}
	entries := l.readdirEntries
	l.readdirMu.Unlock()

	if offset >= uint64(len(entries)) {
		return nil, nil
	}

	var (
		dirents   p9.Dirents
		usedBytes uint64
	)
	for i, de := range entries[offset:] {
		name := de.Name()
		entrySize := uint64(direntHeaderSize + len(name))
		if usedBytes+entrySize > uint64(count) {
			break
		}
		entry := Local{path: path.Join(l.path, name)}
		qid, fi, err := entry.info()
		if err != nil {
			continue
		}
		usedBytes += entrySize
		dirents = append(dirents, p9.Dirent{
			QID:    qid,
			Type:   direntType(fi.Mode()),
			Name:   name,
			Offset: offset + uint64(i) + 1,
		})
	}
	return dirents, nil
}
