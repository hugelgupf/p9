package qids

import (
	"sync/atomic"

	"github.com/hugelgupf/p9/p9"
)

// A PathGenerator allocates paths for a 9P file system.
//
// Generally, QID paths must be unique in a 9P file system (i.e. for a
// p9.Attacher). intro(5) states:
//
//	The thirteen-byte qid fields hold a one-byte type, specifying whether
//	the file is a directory, append-only file, etc., and two unsigned
//	integers: first the four-byte qid version, then the eight-byte qid path.
//	The path is an integer unique among all files in the hierarchy. If a
//	file is deleted and recreated with the same name in the same directory,
//	the old and new path components of the qids should be different.
type PathGenerator struct {
	uids uint64
}

func (g *PathGenerator) NewPath() uint64 {
	return atomic.AddUint64(&g.uids, 1)
}

type Mapper struct {
	g     *PathGenerator
	paths map[uint64]uint64
}

// NewMapper translates given QIDs into the path namespace of g using the path,
// preserving their version and type.
func NewMapper(g *PathGenerator) *Mapper {
	return &Mapper{g: g, paths: make(map[uint64]uint64)}
}

func (m *Mapper) QIDFor(q p9.QID) p9.QID {
	if path, ok := m.paths[q.Path]; ok {
		return p9.QID{
			Type:    q.Type,
			Version: q.Version,
			Path:    path,
		}
	}

	path := m.g.NewPath()
	m.paths[q.Path] = path
	return p9.QID{
		Type:    q.Type,
		Version: q.Version,
		Path:    path,
	}
}

// NewWrapperFile translates all QIDs emanating from f or walked to from f
// using the mapper m.
func NewWrapperFile(f p9.File, m *Mapper) p9.File {
	return qidTransformFile{f, m}
}

type qidTransformFile struct {
	p9.File
	m *Mapper
}

func (q qidTransformFile) Walk(names []string) ([]p9.QID, p9.File, error) {
	qids, file, err := q.File.Walk(names)
	var nqids []p9.QID
	for _, qid := range qids {
		nqids = append(nqids, q.m.QIDFor(qid))
	}
	if file != nil {
		file = qidTransformFile{file, q.m}
	}
	return nqids, file, err
}

func (q qidTransformFile) WalkGetAttr(names []string) ([]p9.QID, p9.File, p9.AttrMask, p9.Attr, error) {
	qids, file, mask, attr, err := q.File.WalkGetAttr(names)
	var nqids []p9.QID
	for _, qid := range qids {
		nqids = append(nqids, q.m.QIDFor(qid))
	}
	if file != nil {
		file = qidTransformFile{file, q.m}
	}
	return nqids, file, mask, attr, err
}

func (q qidTransformFile) GetAttr(req p9.AttrMask) (p9.QID, p9.AttrMask, p9.Attr, error) {
	qid, mask, attr, err := q.File.GetAttr(req)
	return q.m.QIDFor(qid), mask, attr, err
}

func (q qidTransformFile) Open(mode p9.OpenFlags) (p9.QID, uint32, error) {
	qid, iounit, err := q.File.Open(mode)
	return q.m.QIDFor(qid), iounit, err
}

func (q qidTransformFile) Create(name string, flags p9.OpenFlags, permissions p9.FileMode, uid p9.UID, gid p9.GID) (p9.File, p9.QID, uint32, error) {
	file, qid, iounit, err := q.File.Create(name, flags, permissions, uid, gid)
	if file != nil {
		file = qidTransformFile{file, q.m}
	}
	return file, q.m.QIDFor(qid), iounit, err
}

func (q qidTransformFile) Mkdir(name string, permissions p9.FileMode, uid p9.UID, gid p9.GID) (p9.QID, error) {
	qid, err := q.File.Mkdir(name, permissions, uid, gid)
	return q.m.QIDFor(qid), err
}

func (q qidTransformFile) Symlink(oldName string, newName string, uid p9.UID, gid p9.GID) (p9.QID, error) {
	qid, err := q.File.Symlink(oldName, newName, uid, gid)
	return q.m.QIDFor(qid), err
}

func (q qidTransformFile) Mknod(name string, mode p9.FileMode, major uint32, minor uint32, uid p9.UID, gid p9.GID) (p9.QID, error) {
	qid, err := q.File.Mknod(name, mode, major, minor, uid, gid)
	return q.m.QIDFor(qid), err
}

func (q qidTransformFile) Readdir(offset uint64, count uint32) (p9.Dirents, error) {
	dirents, err := q.File.Readdir(offset, count)
	for _, entry := range dirents {
		entry.QID = q.m.QIDFor(entry.QID)
	}
	return dirents, err
}
