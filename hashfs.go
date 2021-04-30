// Copyright (c) 2021, Janoš Guljaš <janos@resenje.org>
// All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fsutil

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	_ fs.FS         = (*HashFS)(nil)
	_ fs.GlobFS     = (*HashFS)(nil)
	_ fs.ReadDirFS  = (*HashFS)(nil)
	_ fs.ReadFileFS = (*HashFS)(nil)
	_ fs.StatFS     = (*HashFS)(nil)
)

// HashFS is a filesystem that injects a hash string into file names from
// another filesystem. If the file name already contains the correct hash in its
// name, filename is not changed. The intended usage is to serve unique
// filenames with http.FileServer in order to have maximal caching period for
// those files.
//
// Method HashedPath provides a way to obtain the filename with a hash in it
// based on the original file name.
type HashFS struct {
	fsys   fs.FS
	hasher Hasher

	hashes   map[string]string
	hashesMu sync.RWMutex
}

// NewHashFS returns a new instance of HashFS.
func NewHashFS(fsys fs.FS, hasher Hasher) *HashFS {
	return &HashFS{
		fsys:   fsys,
		hasher: hasher,
		hashes: make(map[string]string),
	}
}

// Open implements fs.FS interface.
func (s *HashFS) Open(name string) (fs.File, error) {
	canonicalName, hash, err := s.canonicalName(name)
	if err != nil {
		return nil, err
	}
	if hash != "" && canonicalName == name {
		return nil, fs.ErrNotExist
	}
	return s.fsys.Open(canonicalName)
}

// Glob implements fs.GlobFS interface.
func (s *HashFS) Glob(pattern string) ([]string, error) {
	r, err := fs.Glob(s.fsys, pattern)
	if err != nil {
		return nil, err
	}
	var n int
	for _, e := range r {
		canonicalName, hash, err := s.canonicalName(e)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		r[n] = s.hashedPath(canonicalName, hash)
		n++
	}
	return r[:n], nil
}

// ReadDir implements fs.ReadDirFS interface.
func (s *HashFS) ReadDir(name string) ([]fs.DirEntry, error) {
	r, err := fs.ReadDir(s.fsys, name)
	if err != nil {
		return nil, err
	}
	var n int
	for _, e := range r {
		canonicalName, hash, err := s.canonicalName(filepath.Join(name, e.Name()))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		name := s.hashedPath(filepath.Base(canonicalName), hash)
		r[n] = &dirEntry{e: e, name: name}
		n++
	}
	return r[:n], nil
}

// ReadFile implements fs.ReadFileFS interface.
func (s *HashFS) ReadFile(name string) ([]byte, error) {
	canonicalName, hash, err := s.canonicalName(name)
	if err != nil {
		return nil, err
	}
	if hash != "" && canonicalName == name {
		return nil, fs.ErrNotExist
	}
	return fs.ReadFile(s.fsys, canonicalName)
}

// Stat implements fs.StatFS interface.
func (s *HashFS) Stat(name string) (fs.FileInfo, error) {
	canonicalName, hash, err := s.canonicalName(name)
	if err != nil {
		return nil, err
	}
	if hash != "" && canonicalName == name {
		return nil, fs.ErrNotExist
	}
	i, err := fs.Stat(s.fsys, canonicalName)
	if err != nil {
		return nil, err
	}
	return &fileInfo{i: i, name: filepath.Base(name)}, nil
}

// HashedPath returns a path with hash injected into the filename.
func (s *HashFS) HashedPath(name string) (string, error) {
	canonicalName, hash, err := s.canonicalName(name)
	if err != nil {
		return "", err
	}
	return s.hashedPath(canonicalName, hash), nil
}

func (s *HashFS) canonicalName(name string) (canonicalName string, hash string, err error) {
	d, f := path.Split(name)

	parts := strings.Split(f, ".")
	f = ""
	l := len(parts)
	index := 1
	if l > 2 && !(l == 3 && parts[0] == "") {
		index = 2
	}
	var hashFromName string
	for i, part := range parts {
		if i == l-index && s.hasher.IsHash(part) {
			hashFromName = part
			continue
		}
		if i != 0 {
			f += "."
		}
		f += part
	}

	canonicalName = d + f

	hash, err = s.hash(canonicalName)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			hash, err = s.hash(name)
			if err != nil {
				return "", "", err
			}
		} else {
			return "", "", err
		}
	}
	if hashFromName != "" && hashFromName != hash {
		hash, err = s.hash(name)
		if err != nil {
			return "", "", err
		}
		if hashFromName != hash {
			return name, hash, nil
		}
		return name, "", nil
	}

	return canonicalName, hash, nil
}

func (s *HashFS) hashedPath(name, hash string) string {
	if hash == "" {
		return name
	}

	d, f := path.Split(name)

	i := strings.LastIndex(f, ".")
	if i > 0 {
		return d + f[:i] + "." + hash + f[i:]
	}

	return d + f + "." + hash
}

func (s *HashFS) hash(name string) (string, error) {
	s.hashesMu.RLock()
	h, ok := s.hashes[name]
	s.hashesMu.RUnlock()
	if ok {
		return h, nil
	}

	fr, err := s.fsys.Open(name)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer fr.Close()

	fi, err := fr.Stat()
	if err != nil {
		return "", fmt.Errorf("stat file: %w", err)
	}
	if fi.IsDir() {
		return "", fs.ErrNotExist
	}

	h, err = s.hasher.Hash(fr)
	if err != nil {
		return "", fmt.Errorf("hash file: %w", err)
	}

	s.hashesMu.Lock()
	s.hashes[name] = h
	s.hashesMu.Unlock()
	return h, nil
}

var _ fs.DirEntry = (*dirEntry)(nil)

// Name-replacing dir entry.
type dirEntry struct {
	e    fs.DirEntry
	name string
}

func (e *dirEntry) Name() string {
	return e.name
}

func (e *dirEntry) IsDir() bool {
	return e.e.IsDir()
}

func (e *dirEntry) Type() fs.FileMode {
	return e.e.Type()
}

func (e *dirEntry) Info() (fs.FileInfo, error) {
	i, err := e.e.Info()
	if err != nil {
		return nil, err
	}
	return &fileInfo{i: i, name: e.name}, nil
}

var _ fs.FileInfo = (*fileInfo)(nil)

// Name-replacing file info.
type fileInfo struct {
	i    fs.FileInfo
	name string
}

func (i *fileInfo) Name() string {
	return i.name
}

func (i *fileInfo) Size() int64 {
	return i.i.Size()
}

func (i *fileInfo) Mode() fs.FileMode {
	return i.i.Mode()
}

func (i *fileInfo) ModTime() time.Time {
	return i.i.ModTime()
}

func (i *fileInfo) IsDir() bool {
	return i.i.IsDir()
}

func (i *fileInfo) Sys() interface{} {
	return i.i.Sys()
}
