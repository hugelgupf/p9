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

// Package composefs provides a way to compose p9 files and file systems into
// one p9 file server.
package composefs

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hugelgupf/p9/fsimpl/readdir"
	"github.com/hugelgupf/p9/fsimpl/templatefs"
	"github.com/hugelgupf/p9/linux"
	"github.com/hugelgupf/p9/p9"
	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
)

var (
	ErrFlatHierarchy = errors.New("composefs only supports a flat hierarchy")
	ErrFileExists    = errors.New("file already exists")
)

type Opt func(fs *FS) error

// FS is a p9.Attacher.
type FS struct {
	mounts map[string]p9.File
}

func WithMount(dir string, attacher p9.Attacher) Opt {
	return func(fs *FS) error {
		if strings.Contains(dir, string(filepath.Separator)) {
			return fmt.Errorf("%w: directories are not supported: %s", ErrFlatHierarchy, dir)
		}
		if _, ok := fs.mounts[dir]; ok {
			return fmt.Errorf("%w: %s", ErrFileExists, dir)
		}

		f, err := attacher.Attach()
		if err != nil {
			return err
		}
		fs.mounts[dir] = f
		return nil
	}
}

func WithDir(dir string, mounts ...Opt) Opt {
	return func(fs *FS) error {
		subfs, err := New(mounts...)
		if err != nil {
			return err
		}

		return WithMount(dir, subfs)(fs)
	}
}

func WithFile(file string, f p9.File) Opt {
	return func(fs *FS) error {
		if strings.Contains(file, "/") {
			return fmt.Errorf("%w: directories are not supported: %s", ErrFlatHierarchy, file)
		}
		if _, ok := fs.mounts[file]; ok {
			return fmt.Errorf("%w: %s", ErrFileExists, file)
		}

		fs.mounts[file] = f
		return nil
	}
}

func New(mounts ...Opt) (*FS, error) {
	fs := &FS{
		mounts: make(map[string]p9.File),
	}
	for _, m := range mounts {
		if err := m(fs); err != nil {
			return nil, err
		}
	}
	return fs, nil
}

type root struct {
	p9.DefaultWalkGetAttr
	templatefs.ReadOnlyDir
	templatefs.NilCloser

	fs *FS
}

// Attach implements p9.Attacher.Attach.
func (fs *FS) Attach() (p9.File, error) {
	return &root{fs: fs}, nil
}

var (
	_ p9.File     = &root{}
	_ p9.Attacher = &FS{}
)

var (
	rootQID = p9.QID{Type: p9.TypeDir, Path: 1, Version: 0}
)

// Walk implements p9.File.Walk.
func (r *root) Walk(names []string) ([]p9.QID, p9.File, error) {
	if len(names) == 0 {
		return []p9.QID{rootQID}, &root{fs: r.fs}, nil
	}

	file, ok := r.fs.mounts[names[0]]
	if !ok {
		return nil, nil, linux.ENOENT
	}
	// Let the file figure out its own QID.
	return file.Walk(names[1:])
}

// Open implements p9.File.Open.
func (r *root) Open(mode p9.OpenFlags) (p9.QID, uint32, error) {
	if mode.Mode() != p9.ReadOnly {
		return p9.QID{}, 0, linux.EACCES
	}
	return p9.QID{}, 0, nil
}

// Readdir implements p9.File.Readdir.
func (r *root) Readdir(offset uint64, count uint32) (p9.Dirents, error) {
	names := maps.Keys(r.fs.mounts)
	slices.Sort(names)

	qids := make(map[string]p9.QID)
	for _, name := range names {
		qid, _, _, err := r.fs.mounts[name].GetAttr(p9.AttrMask{Mode: true})
		if err != nil {
			return p9.Dirents{}, err
		}
		qids[name] = qid
	}
	return readdir.Readdir(offset, count, names, qids)
}

// StatFS implements p9.File.StatFS.
func (*root) StatFS() (p9.FSStat, error) {
	return p9.FSStat{}, linux.ENOSYS
}

// GetAttr implements p9.File.GetAttr.
func (r *root) GetAttr(req p9.AttrMask) (p9.QID, p9.AttrMask, p9.Attr, error) {
	attr := p9.Attr{
		Mode:      p9.FileMode(0777) | p9.ModeDirectory,
		NLink:     p9.NLink(1 + len(r.fs.mounts)),
		BlockSize: uint64(4096),
	}
	return rootQID, req, attr, nil
}
