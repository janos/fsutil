// Copyright (c) 2021, Janoš Guljaš <janos@resenje.org>
// All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fsutil_test

import (
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"reflect"
	"testing"
	"time"

	"resenje.org/fsutil"
)

const permUserWrite fs.FileMode = 0o200

var (
	//go:embed testdata
	testdataFS      embed.FS
	testdataEmptyFS embed.FS // just an empty filesystem
	assetsFS        = fsutil.MustSub(testdataFS, "testdata")
)

var (
	fileName      = "assets/main.abcd12.css"
	fileContent   = "body { color: green; }"
	fileInfo, _   = fs.Stat(assetsFS, fileName)
	dirEntries, _ = fs.ReadDir(assetsFS, "assets")
)

func TestBackupFS(t *testing.T) {
	backupDir := t.TempDir()

	fsys, err := fsutil.NewBackupFS(assetsFS, backupDir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	testOpen(t, fsys, fileName, fileContent)
	testGlob(t, fsys, "assets/*.css", []string{fileName})
	testReadDir(t, fsys, "assets", dirEntries, 0)
	testReadFile(t, fsys, fileName, fileContent)
	testStat(t, fsys, fileName, fileInfo, 0)
}

func TestBackupFS_NotExist(t *testing.T) {
	backupDir := t.TempDir()

	fsys, err := fsutil.NewBackupFS(assetsFS, backupDir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	testOpenNotExist(t, fsys, "someOtherName.txt")
	testGlob(t, fsys, "someOtherName.*", []string{})
	testReadDirNotExist(t, fsys, "some/Directory")
	testReadFileNotExist(t, fsys, "someOtherName.txt")
	testStatNotExist(t, fsys, "someOtherName.txt")
}

func TestBackupFS_expiry(t *testing.T) {
	backupDir := t.TempDir()

	fsys, err := fsutil.NewBackupFS(assetsFS, backupDir, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-fsys.Cleaned():
		if err := fsys.CleaningErr(); err != nil {
			t.Errorf("clean error: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Error("timeout waiting for backup to be cleaned")
	}

	testOpen(t, fsys, fileName, fileContent)
	testGlob(t, fsys, "assets/*.css", []string{fileName})
	testReadDir(t, fsys, "assets", dirEntries, 0)
	testReadFile(t, fsys, fileName, fileContent)
	testStat(t, fsys, fileName, fileInfo, 0)
}

func TestBackupFS_fromBackup(t *testing.T) {
	backupDir := t.TempDir()

	if _, err := fsutil.NewBackupFS(assetsFS, backupDir, time.Hour); err != nil {
		t.Fatal(err)
	}

	fsys, err := fsutil.NewBackupFS(testdataEmptyFS, backupDir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	testOpen(t, fsys, fileName, fileContent)
	testGlob(t, fsys, "assets/*.css", []string{fileName})
	testReadDir(t, fsys, "assets", dirEntries, permUserWrite)
	testReadFile(t, fsys, fileName, fileContent)
	testStat(t, fsys, fileName, fileInfo, permUserWrite)
}

func TestBackupFS_fromBackup_afterTimeout(t *testing.T) {
	backupDir := t.TempDir()

	if _, err := fsutil.NewBackupFS(assetsFS, backupDir, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}

	fsys, err := fsutil.NewBackupFS(testdataEmptyFS, backupDir, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-fsys.Cleaned():
		if err := fsys.CleaningErr(); err != nil {
			t.Errorf("clean error: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Error("timeout waiting for backup to be cleaned")
	}

	testOpenNotExist(t, fsys, fileName)
	testGlob(t, fsys, "assets/*.css", []string{})
	testReadDirNotExist(t, fsys, "assets")
	testReadFileNotExist(t, fsys, fileName)
	testStatNotExist(t, fsys, fileName)
}

func TestBackupFS_overwriteFiles(t *testing.T) {
	backupDir := t.TempDir()

	if _, err := fsutil.NewBackupFS(assetsFS, backupDir, time.Hour); err != nil {
		t.Fatal(err)
	}

	fsys, err := fsutil.NewBackupFS(assetsFS, backupDir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	testOpen(t, fsys, fileName, fileContent)
	testGlob(t, fsys, "assets/*.css", []string{fileName})
	testReadDir(t, fsys, "assets", dirEntries, 0)
	testReadFile(t, fsys, fileName, fileContent)
	testStat(t, fsys, fileName, fileInfo, 0)
}

func testOpen(t *testing.T, fsys fs.FS, name, wantContent string) {
	t.Helper()

	f, err := fsys.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != wantContent {
		t.Errorf("got content %q, want %q", string(content), wantContent)
	}
}

func testOpenNotExist(t *testing.T, fsys fs.FS, name string) {
	t.Helper()

	if _, err := fsys.Open(name); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func testGlob(t *testing.T, fsys fs.GlobFS, pattern string, want []string) {
	t.Helper()

	got, err := fsys.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func testReadFile(t *testing.T, fsys fs.ReadFileFS, name, wantContent string) {
	t.Helper()

	content, err := fsys.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != wantContent {
		t.Errorf("got content %q, want %q", string(content), wantContent)
	}
}

func testReadFileNotExist(t *testing.T, fsys fs.ReadFileFS, name string) {
	t.Helper()

	if _, err := fsys.ReadFile(name); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func testReadDir(t *testing.T, fsys fs.ReadDirFS, dir string, want []fs.DirEntry, additionalPerm fs.FileMode) {
	t.Helper()

	got, err := fsys.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Errorf("got %v elements, want %v", len(got), len(want))
		return
	}
	for i, e := range got {
		if e.Name() != want[i].Name() {
			t.Errorf("got Name %v, want %v", e.Name(), want[i].Name())
		}
		if e.IsDir() != want[i].IsDir() {
			t.Errorf("got IsDir %v, want %v", e.IsDir(), want[i].IsDir())
		}
		if e.Type() != want[i].Type() {
			t.Errorf("got Type %v, want %v", e.Type(), want[i].Type())
		}
		gotFileInfo, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		wantFileInfo, err := want[i].Info()
		if err != nil {
			t.Fatal(err)
		}
		testFileInfo(t, gotFileInfo, wantFileInfo, additionalPerm)
	}
}

func testReadDirNotExist(t *testing.T, fsys fs.FS, name string) {
	t.Helper()

	if _, err := fs.ReadDir(fsys, name); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func testStat(t *testing.T, fsys fs.StatFS, name string, wantStat fs.FileInfo, additionalPerm fs.FileMode) {
	t.Helper()

	stat, err := fsys.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	testFileInfo(t, stat, wantStat, additionalPerm)
}

func testStatNotExist(t *testing.T, fsys fs.StatFS, name string) {
	t.Helper()

	if _, err := fsys.Stat(name); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func testFileInfo(t *testing.T, got, want fs.FileInfo, additionalPerm fs.FileMode) {
	t.Helper()

	if got.Name() != want.Name() {
		t.Errorf("got Name %v, want %v", got.Name(), want.Name())
	}
	if got.IsDir() != want.IsDir() {
		t.Errorf("got IsDir %v, want %v", got.IsDir(), want.IsDir())
	}
	if got.Mode() != want.Mode()|additionalPerm {
		t.Errorf("got Mode() %v, want %v", got.Mode(), want.Mode()|additionalPerm)
	}
	if got.Size() != want.Size() {
		t.Errorf("got Size %v, want %v", got.Size(), want.Size())
	}
	// ModTime is not preserved.
}

func Test_uniqueStrings(t *testing.T) {
	for _, tc := range []struct {
		name string
		arg  []string
		want []string
	}{
		{
			name: "nil",
		},
		{
			name: "empty",
			arg:  make([]string, 0),
			want: make([]string, 0),
		},
		{
			name: "one element",
			arg:  []string{"a"},
			want: []string{"a"},
		},
		{
			name: "multiple unique element",
			arg:  []string{"a", "b", "c"},
			want: []string{"a", "b", "c"},
		},
		{
			name: "multiple element first twice",
			arg:  []string{"a", "a", "b", "c"},
			want: []string{"a", "b", "c"},
		},
		{
			name: "multiple element second twice",
			arg:  []string{"a", "b", "b", "c"},
			want: []string{"a", "b", "c"},
		},
		{
			name: "multiple element last twice",
			arg:  []string{"a", "b", "c", "c"},
			want: []string{"a", "b", "c"},
		},
		{
			name: "multiple element multiple",
			arg:  []string{"a", "b", "b", "b", "b", "c"},
			want: []string{"a", "b", "c"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := fsutil.UniqueStrings(tc.arg); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("uniqueStrings() = %v, want %v", got, tc.want)
			}
		})
	}
}

func Test_uniqueDirEntry(t *testing.T) {
	for _, tc := range []struct {
		name string
		arg  []fs.DirEntry
		want []fs.DirEntry
	}{
		{
			name: "nil",
		},
		{
			name: "empty",
			arg:  make([]fs.DirEntry, 0),
			want: make([]fs.DirEntry, 0),
		},
		{
			name: "one element",
			arg:  []fs.DirEntry{dir("a")},
			want: []fs.DirEntry{dir("a")},
		},
		{
			name: "multiple unique element",
			arg:  []fs.DirEntry{dir("a"), dir("b"), dir("c")},
			want: []fs.DirEntry{dir("a"), dir("b"), dir("c")},
		},
		{
			name: "multiple element first twice",
			arg:  []fs.DirEntry{dir("a"), dir("a"), dir("b"), dir("c")},
			want: []fs.DirEntry{dir("a"), dir("b"), dir("c")},
		},
		{
			name: "multiple element second twice",
			arg:  []fs.DirEntry{dir("a"), dir("b"), dir("b"), dir("c")},
			want: []fs.DirEntry{dir("a"), dir("b"), dir("c")},
		},
		{
			name: "multiple element last twice",
			arg:  []fs.DirEntry{dir("a"), dir("b"), dir("c"), dir("c")},
			want: []fs.DirEntry{dir("a"), dir("b"), dir("c")},
		},
		{
			name: "multiple element multiple",
			arg:  []fs.DirEntry{dir("a"), dir("b"), dir("b"), dir("b"), dir("b"), dir("c")},
			want: []fs.DirEntry{dir("a"), dir("b"), dir("c")},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := fsutil.UniqueDirEntry(tc.arg); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("uniqueDirEntry() = %v, want %v", got, tc.want)
			}
		})
	}
}

type dirEntry struct {
	name string
	fs.DirEntry
}

func dir(name string) *dirEntry {
	return &dirEntry{
		name: name,
	}
}

func (d *dirEntry) Name() string {
	return d.name
}
