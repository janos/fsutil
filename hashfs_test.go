// Copyright (c) 2021, Janoš Guljaš <janos@resenje.org>
// All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fsutil_test

import (
	"bytes"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"resenje.org/fsutil"
)

var (
	//go:embed testdata/hashfs
	testdataHashFS embed.FS
	assetsHashFS   = fsutil.MustSub(testdataHashFS, "testdata/hashfs")
)

func TestHashFS(t *testing.T) {
	fsys := fsutil.NewHashFS(assetsHashFS, fsutil.NewMD5Hasher(6))

	// Open
	testOpen(t, fsys, "assets/main.45b416.css", "body { color: green; }")
	testOpen(t, fsys, "assets/main.8559e1.css", "body { color: blue; }")
	testOpen(t, fsys, "assets", "")
	testOpenNotExist(t, fsys, "assets/main.012345.css")
	testOpenNotExist(t, fsys, "assets/main.css")
	testOpenNotExist(t, fsys, "passwords.txt")

	// Glob
	testGlob(t, fsys, "assets/*", []string{
		"assets/main.012345.847f70.css",
		"assets/main.45b416.css",
		"assets/main.8559e1.css",
		"assets/subdir",
	})

	// ReadDir
	dirEntries, err := fs.ReadDir(assetsHashFS, "assets")
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range dirEntries {
		if e.Name() == "main.css" {
			dirEntries[i] = fsutil.NewDirEntry(e, "main.8559e1.css")
		}
		if e.Name() == "main.012345.css" {
			dirEntries[i] = fsutil.NewDirEntry(e, "main.012345.847f70.css")
		}
	}
	testReadDir(t, fsys, "assets", dirEntries, 0)
	testReadDirNotExist(t, fsys, "passwords")

	// ReadFile
	testReadFile(t, fsys, "assets/main.45b416.css", "body { color: green; }")
	testReadFile(t, fsys, "assets/main.8559e1.css", "body { color: blue; }")
	testReadFile(t, fsys, "assets/main.012345.847f70.css", "/* file with an invalid hash */")
	testReadFileNotExist(t, fsys, "assets/main.012345.css")
	testReadFileNotExist(t, fsys, "assets/main.css")
	testReadFileNotExist(t, fsys, "passwords.txt")

	// Stat
	fileInfo, err := fs.Stat(assetsHashFS, "assets/main.45b416.css")
	if err != nil {
		t.Fatal(err)
	}
	testStat(t, fsys, "assets/main.45b416.css", fileInfo, 0)
	fileInfo, err = fs.Stat(assetsHashFS, "assets/main.css")
	if err != nil {
		t.Fatal(err)
	}
	testStat(t, fsys, "assets/main.8559e1.css", fsutil.NewFileInfo(fileInfo, "main.8559e1.css"), 0)
	fileInfo, err = fs.Stat(assetsHashFS, "assets/main.012345.css")
	if err != nil {
		t.Fatal(err)
	}
	testStat(t, fsys, "assets/main.012345.847f70.css", fsutil.NewFileInfo(fileInfo, "main.012345.847f70.css"), 0)
	fileInfo, err = fs.Stat(assetsHashFS, "assets")
	if err != nil {
		t.Fatal(err)
	}
	testStat(t, fsys, "assets", fileInfo, 0)
	testStatNotExist(t, fsys, "assets/main.012345.css")
	testStatNotExist(t, fsys, "assets/main.css")
	testStatNotExist(t, fsys, "passwords.txt")

	// HashedPath
	testHashedPath(t, fsys, "assets/main.css", "assets/main.8559e1.css")
	testHashedPath(t, fsys, "assets/main.8559e1.css", "assets/main.8559e1.css")
	testHashedPath(t, fsys, "assets/main.45b416.css", "assets/main.45b416.css")
	testHashedPath(t, fsys, "assets/main.012345.css", "assets/main.012345.847f70.css")
	testHashedPath(t, fsys, "assets", "assets")
	if _, err := fsys.HashedPath("passwords.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("got error %v for file %q, want %v", err, "passwords.txt", fs.ErrNotExist)
	}
}

func TestHashFS_File_ReadDir(t *testing.T) {
	dir := t.TempDir()

	fsys := fsutil.NewHashFS(os.DirFS(dir), fsutil.NewMD5Hasher(8))

	if err := os.MkdirAll(filepath.Join(dir, "assets"), os.ModePerm); err != nil {
		t.Fatal(err)
	}

	var files []string
	for i, b := 0, make([]byte, 12); i < 20; i++ {
		if _, err := rand.Read(b); err != nil {
			t.Fatal(err)
		}
		name := hex.EncodeToString(b)
		f, err := os.OpenFile(filepath.Join(dir, "assets", name), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o666)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := rand.Read(b); err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(f, bytes.NewReader(b)); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		hashedName, err := fsys.HashedPath(filepath.ToSlash(filepath.Join("assets", name)))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, filepath.Base(hashedName))
	}

	sort.Strings(files)

	t.Run("all", func(t *testing.T) {
		f, err := fsys.Open("assets")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()

		fd := f.(fs.ReadDirFile)

		r, err := fd.ReadDir(-1)
		if err != nil {
			t.Fatal(err)
		}

		var got []string
		for _, e := range r {
			got = append(got, e.Name())
		}
		sort.Strings(got)

		if fmt.Sprint(got) != fmt.Sprint(files) {
			t.Errorf("got files %v, want %v", got, files)
		}
	})

	t.Run("partial", func(t *testing.T) {
		f, err := fsys.Open("assets")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()

		fd := f.(fs.ReadDirFile)

		var got []string
		for {
			r, err := fd.ReadDir(rand.Intn(7))
			if err != nil && !errors.Is(err, io.EOF) {
				t.Fatal(err)
			}
			for _, e := range r {
				got = append(got, e.Name())
			}
			if errors.Is(err, io.EOF) {
				break
			}
		}
		sort.Strings(got)

		if fmt.Sprint(got) != fmt.Sprint(files) {
			t.Errorf("got files %v, want %v", got, files)
		}
	})
}

func testHashedPath(t *testing.T, fsys *fsutil.HashFS, name, hashedName string) {
	hashedPath, err := fsys.HashedPath(name)
	if err != nil {
		t.Fatal(err)
	}
	if hashedPath != hashedName {
		t.Errorf("got hashed path %q, want %q", hashedPath, hashedName)
	}
}
