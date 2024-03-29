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
	"strings"

	"github.com/hugelgupf/p9/fsimpl/qids"
	"github.com/hugelgupf/p9/fsimpl/readdir"
	"github.com/hugelgupf/p9/fsimpl/templatefs"
	"github.com/hugelgupf/p9/linux"
	"github.com/hugelgupf/p9/p9"
	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
)

// Option is a configurator for New.
type Option func(*attacher) error

// WithFile includes the file named name with file contents content in the file system.
func WithFile(name, content string) Option {
	return func(a *attacher) error {
		if strings.Contains(name, "/") {
			return fmt.Errorf("directories are not supported by staticfs")
		}
		if _, ok := a.files[name]; ok {
			return fmt.Errorf("file named %q already exists", name)
		}

		f := qids.NewWrapperFile(ReadOnlyFile(content), qids.NewMapper(a.paths))
		a.files[name] = f

		// Precompute QID for Readdir.
		qid, _, _, err := f.GetAttr(p9.AttrMask{Mode: true})
		if err != nil {
			return err
		}
		a.qids[name] = qid
		return nil
	}
}

// New creates a new read-only static file system defined by the files passed
// with opts.
//
// staticfs only supports one directory with regular files.
func New(opts ...Option) (p9.Attacher, error) {
	a := &attacher{
		files: make(map[string]p9.File),
		qids:  make(map[string]p9.QID),
		paths: &qids.PathGenerator{},
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
	files map[string]p9.File
	qids  map[string]p9.QID

	paths *qids.PathGenerator
}

// Attach implements p9.Attacher.Attach.
func (a *attacher) Attach() (p9.File, error) {
	return &dir{a: a}, nil
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
	templatefs.ReadOnlyDir
	templatefs.NilCloser

	a *attacher
}

var (
	// PathGenerator leaves Path: 0 unused.
	rootQID = p9.QID{Type: p9.TypeDir, Path: 0, Version: 0}
)

var _ p9.File = &dir{}

// Open implements p9.File.Open.
func (d *dir) Open(mode p9.OpenFlags) (p9.QID, uint32, error) {
	if mode.Mode() == p9.ReadOnly {
		return rootQID, 4096, nil
	}
	return p9.QID{}, 0, linux.EROFS
}

// Walk implements p9.File.Walk.
func (d *dir) Walk(names []string) ([]p9.QID, p9.File, error) {
	switch len(names) {
	case 0:
		return nil, d, nil

	case 1:
		file, ok := d.a.files[names[0]]
		if !ok {
			return nil, nil, linux.ENOENT
		}
		return []p9.QID{d.a.qids[names[0]]}, file, nil

	default:
		return nil, nil, linux.ENOENT
	}
}

// GetAttr implements p9.File.GetAttr.
func (d *dir) GetAttr(req p9.AttrMask) (p9.QID, p9.AttrMask, p9.Attr, error) {
	return rootQID, req, p9.Attr{
		Mode:  p9.ModeDirectory | 0666,
		UID:   0,
		GID:   0,
		NLink: 2,
	}, nil
}

// Readdir implements p9.File.Readdir.
func (d *dir) Readdir(offset uint64, count uint32) (p9.Dirents, error) {
	names := maps.Keys(d.a.files)
	slices.Sort(names)
	return readdir.Readdir(offset, count, names, d.a.qids)
}

// ReadOnlyFile returns a read-only p9.File using a QID with path 0.
func ReadOnlyFile(content string) p9.File {
	return &file{
		Reader: strings.NewReader(content),
		qid: p9.QID{
			Type:    p9.TypeRegular,
			Version: 0,
			Path:    0,
		},
	}
}

// file is a read-only file.
type file struct {
	statfs
	p9.DefaultWalkGetAttr
	templatefs.ReadOnlyFile
	templatefs.NilCloser

	*strings.Reader

	qid p9.QID
}

var _ p9.File = &file{}

// Walk implements p9.File.Walk.
func (f *file) Walk(names []string) ([]p9.QID, p9.File, error) {
	if len(names) == 0 {
		return nil, f, nil
	}
	return nil, nil, linux.ENOTDIR
}

// Open implements p9.File.Open.
func (f *file) Open(mode p9.OpenFlags) (p9.QID, uint32, error) {
	if mode.Mode() == p9.ReadOnly {
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
