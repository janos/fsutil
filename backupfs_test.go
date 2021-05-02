// Copyright (c) 2021, Janoš Guljaš <janos@resenje.org>
// All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fsutil_test

import (
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"
	"time"

	"resenje.org/fsutil"
)

const (
	permUserWrite fs.FileMode = 0o200
	permAllrite   fs.FileMode = 0o222
)

var (
	//go:embed testdata/backupfs
	testdataBackupFS embed.FS
	assetsBackupFS   = fsutil.MustSub(testdataBackupFS, "testdata/backupfs")
)

func TestBackupFS(t *testing.T) {
	backupDir := t.TempDir()

	fsys, err := fsutil.NewBackupFS(assetsBackupFS, backupDir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	fileName, fileContent, fileInfo, dirEntries := backupFSFiles(t)

	testOpen(t, fsys, fileName, fileContent)
	testGlob(t, fsys, "assets/*.css", []string{fileName})
	testReadDir(t, fsys, "assets", dirEntries, 0)
	testReadFile(t, fsys, fileName, fileContent)
	testStat(t, fsys, fileName, fileInfo, 0)

	testOpenNotExist(t, fsys, "someOtherName.txt")
	testGlob(t, fsys, "someOtherName.*", []string{})
	testReadDirNotExist(t, fsys, "some/Directory")
	testReadFileNotExist(t, fsys, "someOtherName.txt")
	testStatNotExist(t, fsys, "someOtherName.txt")
}

func TestBackupFS_expiry(t *testing.T) {
	backupDir := t.TempDir()

	fsys, err := fsutil.NewBackupFS(assetsBackupFS, backupDir, 10*time.Millisecond)
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

	fileName, fileContent, fileInfo, dirEntries := backupFSFiles(t)

	testOpen(t, fsys, fileName, fileContent)
	testGlob(t, fsys, "assets/*.css", []string{fileName})
	testReadDir(t, fsys, "assets", dirEntries, 0)
	testReadFile(t, fsys, fileName, fileContent)
	testStat(t, fsys, fileName, fileInfo, 0)
}

func TestBackupFS_fromBackup(t *testing.T) {
	backupDir := t.TempDir()

	if _, err := fsutil.NewBackupFS(assetsBackupFS, backupDir, time.Hour); err != nil {
		t.Fatal(err)
	}

	fsys, err := fsutil.NewBackupFS(new(embed.FS), backupDir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	var additionalPerm fs.FileMode
	if runtime.GOOS == "windows" {
		additionalPerm = permAllrite
	} else {
		additionalPerm = permUserWrite
	}

	fileName, fileContent, fileInfo, dirEntries := backupFSFiles(t)

	testOpen(t, fsys, fileName, fileContent)
	testGlob(t, fsys, "assets/*.css", []string{fileName})
	testReadDir(t, fsys, "assets", dirEntries, additionalPerm)
	testReadFile(t, fsys, fileName, fileContent)
	testStat(t, fsys, fileName, fileInfo, additionalPerm)
}

func TestBackupFS_fromBackup_afterTimeout(t *testing.T) {
	backupDir := t.TempDir()

	if _, err := fsutil.NewBackupFS(assetsBackupFS, backupDir, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}

	fsys, err := fsutil.NewBackupFS(new(embed.FS), backupDir, 10*time.Millisecond)
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

	fileName, _, _, _ := backupFSFiles(t)

	testOpenNotExist(t, fsys, fileName)
	testGlob(t, fsys, "assets/*.css", []string{})
	testReadDirNotExist(t, fsys, "assets")
	testReadFileNotExist(t, fsys, fileName)
	testStatNotExist(t, fsys, fileName)
}

func TestBackupFS_overwriteFiles(t *testing.T) {
	backupDir := t.TempDir()

	if _, err := fsutil.NewBackupFS(assetsBackupFS, backupDir, time.Hour); err != nil {
		t.Fatal(err)
	}

	fsys, err := fsutil.NewBackupFS(assetsBackupFS, backupDir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	fileName, fileContent, fileInfo, dirEntries := backupFSFiles(t)

	testOpen(t, fsys, fileName, fileContent)
	testGlob(t, fsys, "assets/*.css", []string{fileName})
	testReadDir(t, fsys, "assets", dirEntries, 0)
	testReadFile(t, fsys, fileName, fileContent)
	testStat(t, fsys, fileName, fileInfo, 0)
}

func backupFSFiles(t *testing.T) (fileName, fileContent string, fileInfo fs.FileInfo, dirEntries []fs.DirEntry) {
	t.Helper()

	fileName = "assets/main.45b416.css"

	fileInfo, err := fs.Stat(assetsBackupFS, fileName)
	if err != nil {
		t.Fatal(err)
	}
	dirEntries, err = fs.ReadDir(assetsBackupFS, "assets")
	if err != nil {
		t.Fatal(err)
	}
	return fileName, "body { color: green; }", fileInfo, dirEntries
}

func testOpen(t *testing.T, fsys fs.FS, name, wantContent string) {
	t.Helper()

	f, err := fsys.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	var content []byte
	if !fi.IsDir() {
		content, err = io.ReadAll(f)
		if err != nil {
			t.Fatal(err)
		}
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
		t.Fatal("read dir", err)
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
			t.Fatal("got info", err)
		}
		wantFileInfo, err := want[i].Info()
		if err != nil {
			t.Fatal("want info", err)
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

func TestBackupFS_File_ReadDir(t *testing.T) {
	dir := t.TempDir()
	backupDir := t.TempDir()

	if err := os.Mkdir(filepath.Join(dir, "assets"), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(backupDir, "assets"), 0o777); err != nil {
		t.Fatal(err)
	}

	var files []string
	var fsFiles []string
	for i, b := 0, make([]byte, 12); i < 20; i++ {
		if _, err := rand.Read(b); err != nil {
			t.Fatal(err)
		}
		name := hex.EncodeToString(b)
		d := dir
		if i%3 != 0 {
			d = backupDir
		}
		f, err := os.Create(filepath.Join(d, "assets", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		files = append(files, name)
		if d == dir {
			fsFiles = append(fsFiles, name)
		}
	}

	sort.Strings(files)
	sort.Strings(fsFiles)

	t.Run("all", func(t *testing.T) {
		fsys, err := fsutil.NewBackupFS(os.DirFS(dir), backupDir, time.Hour)
		if err != nil {
			t.Fatal(err)
		}

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

		if fmt.Sprint(got) != fmt.Sprint(files) {
			t.Errorf("got files %v, want %v", got, files)
		}
	})

	t.Run("all after expire", func(t *testing.T) {
		fsys, err := fsutil.NewBackupFS(os.DirFS(dir), backupDir, 0)
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
		if fmt.Sprint(got) != fmt.Sprint(fsFiles) {
			t.Errorf("got files %v, want %v", got, fsFiles)
		}
	})
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
