// Copyright (c) 2020, Janoš Guljaš <janos@resenje.org>
// All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package fsutil implements some filesystem utility functions.
package fsutil

import (
	"io/fs"
)

// FSFunc type is an adapter to allow the use of ordinary functions as
// filesystems. If f is a function with the appropriate signature, FSFunc(f) is
// a FS that calls f.
type FSFunc func(name string) (fs.File, error)

// Open implements fs.FS type.
func (f FSFunc) Open(name string) (fs.File, error) {
	return f(name)
}

// MustSub constructs a new filesystem as a sub-directory of an existing
// filesystem. It panics if it fs.Sub returns an error.
func MustSub(fsys fs.FS, dir string) fs.FS {
	if dir == "" {
		return fsys
	}
	f, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return f
}

// NoDirsFS constructs a new filesystems that does not return directories.
func NoDirsFS(fsys fs.FS) fs.FS {
	return FSFunc(func(name string) (fs.File, error) {
		f, err := fsys.Open(name)
		if err != nil {
			return nil, err
		}
		info, err := f.Stat()
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			return nil, fs.ErrNotExist
		}
		return f, nil
	})
}

// ReadFileFS constructs a filesystem with ReadFile method. Even though the
// ReadFile method just using Open method on the provided filesystem, this
// function is useful as an adapter where fs.ReadFileFS is needed.
func ReadFileFS(fsys fs.FS) fs.ReadFileFS {
	return readFileFS{fsys: fsys}
}

type readFileFS struct {
	fsys fs.FS
}

func (f readFileFS) Open(name string) (fs.File, error) {
	return f.fsys.Open(name)
}

func (f readFileFS) ReadFile(name string) (data []byte, err error) {
	return fs.ReadFile(f.fsys, name)
}