// Copyright (c) 2021, Janoš Guljaš <janos@resenje.org>
// All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fsutil_test

import (
	"embed"
	"errors"
	"io/fs"
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
	testOpenNotExist(t, fsys, "assets/main.012345.css")
	testOpenNotExist(t, fsys, "assets/main.css")
	testOpenNotExist(t, fsys, "passwords.txt")
	testOpenNotExist(t, fsys, "assets")

	// Glob
	testGlob(t, fsys, "assets/*.css", []string{
		"assets/main.012345.847f70.css",
		"assets/main.45b416.css",
		"assets/main.8559e1.css",
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
	testReadFileNotExist(t, fsys, "assets")

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
	testStatNotExist(t, fsys, "assets/main.012345.css")
	testStatNotExist(t, fsys, "assets/main.css")
	testStatNotExist(t, fsys, "passwords.txt")
	testStatNotExist(t, fsys, "assets")

	// HashedPath
	testHashedPath(t, fsys, "assets/main.css", "assets/main.8559e1.css")
	testHashedPath(t, fsys, "assets/main.8559e1.css", "assets/main.8559e1.css")
	testHashedPath(t, fsys, "assets/main.45b416.css", "assets/main.45b416.css")
	testHashedPath(t, fsys, "assets/main.012345.css", "assets/main.012345.847f70.css")
	for _, p := range []string{
		"passwords.txt",
		"assets",
	} {
		if _, err := fsys.HashedPath(p); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("got error %v for file %q, want %v", err, p, fs.ErrNotExist)
		}
	}
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
