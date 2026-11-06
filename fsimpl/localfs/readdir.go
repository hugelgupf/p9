package localfs

import (
	"errors"
	"io"
	"path"

	"github.com/hugelgupf/p9/linux"
	"github.com/hugelgupf/p9/p9"
)

type directoryPage struct {
	overflow     []string
	next         uint64
	biggestEntry *uint64
}

const (
	qidSize         = 13 // qid[13]
	offsetSize      = 8  // offset[8]
	typeSize        = 1  // type[1]
	sringHeaderSize = 2  // length prefix for name[s]
	minDirentSize   = qidSize + offsetSize +
		typeSize + sringHeaderSize
	minimumNameLen = 1
)

// Readdir implements p9.File.Readdir.
func (l *Local) Readdir(offset uint64, count uint32) (p9.Dirents, error) {
	if count < minDirentSize+minimumNameLen {
		return nil, linux.EINVAL
	}
	chunkSize, err := l.getChunkSize(offset, count)
	if err != nil {
		return nil, err
	}
	if rewindDir := offset == 0 &&
		l.next != 0; rewindDir {
		if err := l.rewindDir(); err != nil {
			return nil, err
		}
	}
	var (
		p9Ents p9.Dirents
		next   = l.next
		total  uint32
	)
	for {
		names, lastChunk, err := l.getNextNames(chunkSize)
		if len(names) == 0 ||
			err != nil {
			return p9Ents, err
		}
		entries := make(p9.Dirents, len(names))
		for i, name := range names {
			size := p9.DirentSize(name)
			total += size
			if total > count {
				if len(p9Ents) == 0 {
					return nil, linux.EINVAL // `count` too small to fit first entry.
				}
				l.next = next
				l.overflow = names[i:]
				return p9Ents, nil
			}
			var (
				qid      p9.QID
				localEnt = Local{path: path.Join(l.path, name)}
			)
			qid, _, err := localEnt.info()
			if err != nil {
				l.next = next
				l.overflow = names[i:]
				return p9Ents, err
			}
			next += uint64(size)
			entries[i] = p9.Dirent{
				QID:    qid,
				Type:   qid.Type,
				Name:   name,
				Offset: next,
			}
		}
		l.overflow = nil
		l.next = entries[len(entries)-1].Offset
		p9Ents = append(p9Ents, entries...)
		if !lastChunk {
			remainder := count - uint32(total)
			if chunkSize, err = l.estimateChunkSize(remainder); err != nil {
				return nil, err
			}
		}
	}
}

func (l *Local) getChunkSize(offset uint64, count uint32) (int, error) {
	if offset == 0 {
		return 1, nil // "Peek" is a common enough request.
	}
	return l.estimateChunkSize(count)
}

func (l *Local) rewindDir() error {
	const dirOffset, whence = 0, io.SeekStart
	_, err := l.file.Seek(dirOffset, whence)
	if err == nil {
		l.next = 0
		l.overflow = nil
	}
	return err
}

func (l *Local) estimateChunkSize(count uint32) (int, error) {
	if l.biggestEntry == nil { // TODO: abstract better.
		var maxNameSize uint32
		if stat, err := l.StatFS(); err == nil {
			if maxNameSize = stat.NameLength; maxNameSize == 0 {
				maxNameSize = 255 // Fallback; Common value.
			}
		} else {
			if !errors.Is(err, linux.ENOSYS) {
				return -1, err
			}
			maxNameSize = 255
		}
		biggestEntry := direntSize(int(maxNameSize))
		l.biggestEntry = &biggestEntry
	}
	estimate := min(
		max(
			uint64(count)/(*l.biggestEntry),
			1, // At least 1.
		),
		256, // At most 256.
	)
	return int(estimate), nil
}

// direntSize computes the 9P2000.L wire size for a dirent.
func direntSize(nameLen int) uint64 {
	return uint64(minDirentSize + nameLen)
}

func (l *Local) getNextNames(count int) ([]string, bool, error) {
	names := l.overflow
	if len(names) != 0 {
		return names, false, nil
	}
	var (
		err       error
		lastChunk bool
	)
	if names, err = l.file.Readdirnames(count); err != nil {
		if err != io.EOF {
			return nil, false, err
		}
		lastChunk = true
	}
	if count <= 0 { // Error will be `nil` (not [io.EOF]);
		lastChunk = true // set flag via `n`.
	}
	return names, lastChunk, nil
}
