// Copyright (c) 2020, Janoš Guljaš <janos@resenje.org>
// All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fsutil_test

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"resenje.org/fsutil"
)

var fsys *mockFS // global filesystem used by some of the tests

var (
	errNotFound = errors.New("file not found")
	errTest1    = errors.New("test1")
	errTest2    = errors.New("test2")
)

func TestFSFunc(t *testing.T) {
	name := "file.txt"
	var file fs.File = new(mockFile)

	var gotName string

	fsys := fsutil.FSFunc(func(name string) (fs.File, error) {
		gotName = name
		return file, errTest1
	})

	gotFile, gotErr := fsys.Open(name)
	if gotFile != file {
		t.Errorf("got file %v, want %v", gotFile, file)
	}
	if gotErr != errTest1 {
		t.Errorf("got error %v, want %v", gotErr, errTest1)
	}
	if gotName != name {
		t.Errorf("got name %v, want %v", gotName, name)
	}
}

func init() {
	fsys = newMockFS() // setup global filesystem
	fsys.setFile("README.md", []byte("### fsutils\n\nFilesystem utility functions"), 0, false, nil, nil, nil)
	fsys.setFile("doc.go", []byte("package fsutil"), 0, false, nil, nil, nil)
	fsys.setFile("filesystem", nil, fs.ModeDir, true, nil, nil, nil)
	fsys.setFile("filesystem/doc.go", []byte("package filesystem"), 0, false, nil, nil, nil)
	fsys.setFile("cmd", nil, fs.ModeDir, true, nil, nil, nil)
	fsys.setFile("cmd/fsutil", nil, fs.ModeDir, true, nil, nil, nil)
	fsys.setFile("cmd/fsutil/main.go", []byte("package main"), 0, false, nil, nil, nil)
	fsys.setFile("cmd/fsutil/cmd.go", nil, 0, false, nil, errTest1, nil)
	fsys.setFile("cmd/fsutil/err.go", nil, 0, false, nil, nil, errTest2)
}

func TestSubdirFS(t *testing.T) {
	t.Run("root", func(t *testing.T) {
		sfs := fsutil.SubdirFS(fsys, "")

		for name := range fsys.files {
			assertFile(t, sfs, "", name)
		}
	})

	t.Run("one subdirs", func(t *testing.T) {
		sfs := fsutil.SubdirFS(fsys, "filesystem")

		assertFile(t, sfs, "filesystem", "doc.go")
	})

	t.Run("two subdirs", func(t *testing.T) {
		sfs := fsutil.SubdirFS(fsys, "cmd/fsutil")

		assertFile(t, sfs, "cmd/fsutil", "main.go")
	})
}

func TestNoDirsFS(t *testing.T) {
	ndfs := fsutil.NoDirsFS(fsys)

	for name, f := range fsys.files {
		if f.info.err != nil {
			continue
		}
		info, err := f.Stat()
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			assertFile(t, ndfs, "", name)
			continue
		}
		if _, err := ndfs.Open(name); err != fs.ErrNotExist {
			t.Errorf("got error %v for file %q, want %v", err, name, fs.ErrNotExist)
		}
	}

	t.Run("stat error", func(t *testing.T) {
		if _, err := ndfs.Open("cmd/fsutil/err.go"); err != errTest2 {
			t.Errorf("got error %v, want %v", err, errTest2)
		}
	})
}

func TestReadFileFS(t *testing.T) {
	rffs := fsutil.ReadFileFS(fsys)

	for name, f := range fsys.files {
		assertFile(t, rffs, "", name)

		got, err := rffs.ReadFile(name)
		if err != nil {
			if err != f.err {
				t.Errorf("got error %v reading file %q, want %v", err, name, f.err)
			}
			continue
		}
		want := f.data
		if !bytes.Equal(got, want) {
			t.Errorf("got data %x, want %x", got, want)
		}
	}
}

func assertFile(t *testing.T, sfs fs.FS, dir, name string) {
	t.Helper()

	filename := filepath.Join(dir, name)
	want, ok := fsys.files[filename]
	if !ok {
		t.Fatalf("file %q not in filesystem", filename)
	}

	got, err := sfs.Open(name)
	if err != want.err {
		t.Fatalf("got open error %v, want %v", err, want.err)
	}
	if err == nil && got != want {
		t.Errorf("got file %q %v, want %v", name, got, want)
	}
}

type mockFS struct {
	files map[string]*mockFile
}

func newMockFS() *mockFS {
	return &mockFS{
		files: make(map[string]*mockFile),
	}
}

func (f *mockFS) Open(name string) (fs.File, error) {
	mf, ok := f.files[name]
	if !ok {
		return nil, errNotFound
	}
	if mf.err != nil {
		return nil, mf.err
	}
	return mf, nil
}

func (f *mockFS) setFile(name string, data []byte, mode fs.FileMode, isDir bool, sys interface{}, err, startErr error) {
	f.files[name] = &mockFile{
		info: mockFileInfo{
			name:    name,
			size:    int64(len(data)),
			mode:    mode,
			modTime: time.Now(),
			isDir:   isDir,
			sys:     sys,
			err:     startErr,
		},
		data: data,
		err:  err,
	}
}

type mockFile struct {
	info   mockFileInfo
	data   []byte
	cursor int
	err    error
}

func (f *mockFile) Stat() (fs.FileInfo, error) {
	if f.info.err != nil {
		return nil, f.info.err
	}
	return f.info, nil
}

func (f *mockFile) Read(b []byte) (int, error) {
	if len(f.data) <= f.cursor {
		return 0, io.EOF
	}
	n := copy(b, f.data[f.cursor:])
	f.cursor += n
	return n, nil
}

func (f *mockFile) Close() error {
	return nil
}

type mockFileInfo struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
	isDir   bool
	sys     interface{}
	err     error
}

func (f mockFileInfo) Name() string       { return f.name }
func (f mockFileInfo) Size() int64        { return f.size }
func (f mockFileInfo) Mode() fs.FileMode  { return f.mode }
func (f mockFileInfo) ModTime() time.Time { return f.modTime }
func (f mockFileInfo) IsDir() bool        { return f.isDir }
func (f mockFileInfo) Sys() interface{}   { return f.sys }
