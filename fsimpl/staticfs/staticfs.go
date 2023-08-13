// Copyright 2018 The gVisor Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package staticfs implements a read-only in-memory file system.
package staticfs

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hugelgupf/p9/fsimpl/readdir"
	"github.com/hugelgupf/p9/fsimpl/templatefs"
	"github.com/hugelgupf/p9/linux"
	"github.com/hugelgupf/p9/p9"
)

// Option is a configurator for New.
type Option func(*attacher) error

// WithFile includes the file named name with file contents content in the file system.
func WithFile(name, content string) Option {
	return func(a *attacher) error {
		if strings.Contains(name, "/") {
			return fmt.Errorf("directories are not supported by simplefs")
		}
		if _, ok := a.files[name]; ok {
			return fmt.Errorf("file named %q already exists", name)
		}
		a.files[name] = content
		a.names = append(a.names, name)
		sort.Strings(a.names)
		return nil
	}
}

// New creates a new read-only static file system defined by the files passed
// with opts.
//
// staticfs only supports one directory with regular files.
func New(opts ...Option) (p9.Attacher, error) {
	a := &attacher{
		files: make(map[string]string),
		qids:  &p9.QIDGenerator{},
	}
	for _, o := range opts {
		if err := o(a); err != nil {
			return nil, err
		}
	}
	return a, nil
}

type attacher struct {
	// files maps filenames to file contents.
	files map[string]string

	// names is a sorted list of the keys of files.
	names []string

	qids *p9.QIDGenerator
}

// Attach implements p9.Attacher.Attach.
func (a *attacher) Attach() (p9.File, error) {
	return &dir{
		a:   a,
		qid: a.qids.Get(p9.TypeDir),
	}, nil
}

type statfs struct{}

// StatFS implements p9.File.StatFS.
func (statfs) StatFS() (p9.FSStat, error) {
	return p9.FSStat{
		Type:      0x01021997, /* V9FS_MAGIC */
		BlockSize: 4096,       /* whatever */
	}, nil
}

// dir is the root directory.
type dir struct {
	statfs
	p9.DefaultWalkGetAttr
	templatefs.NotSymlinkFile
	templatefs.ReadOnlyDir
	templatefs.IsDir
	templatefs.NilCloser
	templatefs.NoopRenamed
	templatefs.NotLockable

	qid p9.QID
	a   *attacher
}

// Open implements p9.File.Open.
func (d *dir) Open(mode p9.OpenFlags) (p9.QID, uint32, error) {
	if mode == p9.ReadOnly {
		return d.qid, 4096, nil
	}
	return p9.QID{}, 0, linux.EROFS
}

// Walk implements p9.File.Walk.
func (d *dir) Walk(names []string) ([]p9.QID, p9.File, error) {
	switch len(names) {
	case 0:
		return []p9.QID{d.qid}, d, nil

	case 1:
		content, ok := d.a.files[names[0]]
		if !ok {
			return nil, nil, linux.ENOENT
		}
		qid := d.a.qids.Get(p9.TypeRegular)
		return []p9.QID{qid}, ReadOnlyFile(content, qid), nil
	default:
		return nil, nil, linux.ENOENT
	}
}

// GetAttr implements p9.File.GetAttr.
func (d *dir) GetAttr(req p9.AttrMask) (p9.QID, p9.AttrMask, p9.Attr, error) {
	return d.qid, req, p9.Attr{
		Mode:  p9.ModeDirectory | 0666,
		UID:   0,
		GID:   0,
		NLink: 2,
	}, nil
}

// Readdir implements p9.File.Readdir.
func (d *dir) Readdir(offset uint64, count uint32) (p9.Dirents, error) {
	qids := make(map[string]p9.QID)
	for _, name := range d.a.names {
		qids[name] = d.a.qids.Get(p9.TypeRegular)
	}
	return readdir.Readdir(offset, count, d.a.names, qids)
}

// ReadOnlyFile returns a read-only p9.File.
func ReadOnlyFile(content string, qid p9.QID) p9.File {
	return &file{
		Reader: strings.NewReader(content),
		qid:    qid,
	}
}

// file is a read-only file.
type file struct {
	statfs
	p9.DefaultWalkGetAttr
	templatefs.ReadOnlyFile
	templatefs.NilCloser
	templatefs.NotDirectoryFile
	templatefs.NotSymlinkFile
	templatefs.NoopRenamed
	templatefs.NotLockable

	*strings.Reader

	qid p9.QID
}

// Walk implements p9.File.Walk.
func (f *file) Walk(names []string) ([]p9.QID, p9.File, error) {
	if len(names) == 0 {
		return []p9.QID{f.qid}, f, nil
	}
	return nil, nil, linux.ENOTDIR
}

// Open implements p9.File.Open.
func (f *file) Open(mode p9.OpenFlags) (p9.QID, uint32, error) {
	if mode == p9.ReadOnly {
		return f.qid, 4096, nil
	}
	return p9.QID{}, 0, linux.EROFS
}

// GetAttr implements p9.File.GetAttr.
func (f *file) GetAttr(req p9.AttrMask) (p9.QID, p9.AttrMask, p9.Attr, error) {
	return f.qid, req, p9.Attr{
		Mode:      p9.ModeRegular | 0666,
		UID:       0,
		GID:       0,
		NLink:     0,
		Size:      uint64(f.Reader.Size()),
		BlockSize: 4096, /* whatever? */
	}, nil
}
