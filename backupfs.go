// Copyright (c) 2021, Janoš Guljaš <janos@resenje.org>
// All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fsutil

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	_ fs.FS         = (*BackupFS)(nil)
	_ fs.GlobFS     = (*BackupFS)(nil)
	_ fs.ReadDirFS  = (*BackupFS)(nil)
	_ fs.ReadFileFS = (*BackupFS)(nil)
	_ fs.StatFS     = (*BackupFS)(nil)
)

// BackupFS implements a filesystem which copies all data from another
// filesystem to a directory when it is constructed. It uses it as a backup for
// a given time to live value in case files in the original filesystem change.
// The intended usage is with embedded filesystems to temporary preserve older
// files if they are needed shorty after new embedded filesystem with new files
// is available.
type BackupFS struct {
	fsys          fs.FS
	backup        fs.FS
	cleaned       chan struct{}
	cleaningErr   error
	cleaningErrMu sync.Mutex
}

// NewBackupFS constructs a new BackupFS for another filesystem, that is copied
// in dir with the backup lifetime.
//
// Be aware that the complete dir will be deleted after it is expired. Make sure
// that it does not contain any relevant
func NewBackupFS(fsys fs.FS, dir string, ttl time.Duration) (*BackupFS, error) {
	dir = filepath.Clean(dir)
	if !validateDir(dir) {
		return nil, errors.New("unsupported directory")
	}

	s := new(BackupFS)
	s.fsys = fsys
	s.backup = os.DirFS(dir)
	s.cleaned = make(chan struct{})

	if err := s.copy(dir); err != nil {
		return nil, fmt.Errorf("copy files to the backup directory: %w", err)
	}

	done := make(chan struct{})

	runtime.SetFinalizer(s, func(_ *BackupFS) {
		close(done)
	})

	go func() {
		t := time.NewTimer(ttl)
		defer t.Stop()
		select {
		case <-t.C:
			err := os.RemoveAll(dir)
			s.cleaningErrMu.Lock()
			s.cleaningErr = err
			s.cleaningErrMu.Unlock()
			close(s.cleaned)
		case <-done:
		}
	}()

	return s, nil
}

// Open implements fs.FS interface.
func (s *BackupFS) Open(name string) (fs.File, error) {
	f, err := s.fsys.Open(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			f, err := s.backup.Open(name)
			if err != nil {
				return nil, err
			}
			return newBackupFile(name, f, s.backup), nil
		}
		return nil, err
	}
	return newBackupFile(name, f, s.backup), nil
}

// Glob implements fs.GlobFS interface.
func (s *BackupFS) Glob(pattern string) ([]string, error) {
	r, err := fs.Glob(s.fsys, pattern)
	if err != nil {
		return nil, err
	}
	rc, err := fs.Glob(s.backup, pattern)
	if err != nil {
		return nil, err
	}
	r = append(r, rc...)
	sort.Strings(r)
	return uniqueStrings(r), nil
}

// ReadDir implements fs.ReadDirFS interface.
func (s *BackupFS) ReadDir(name string) ([]fs.DirEntry, error) {
	var doesNotExist bool
	r, err := fs.ReadDir(s.fsys, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			doesNotExist = true
		} else {
			return nil, err
		}
	}
	rc, err := fs.ReadDir(s.backup, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if doesNotExist {
				return nil, err
			}
		} else {
			return nil, err
		}
	}
	r = append(r, rc...)
	sort.SliceStable(r, func(i, j int) bool {
		return r[i].Name() < r[j].Name()
	})
	return uniqueDirEntry(r), nil
}

// ReadFile implements fs.ReadFileFS interface.
func (s *BackupFS) ReadFile(name string) ([]byte, error) {
	data, err := fs.ReadFile(s.fsys, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fs.ReadFile(s.backup, name)
		}
		return nil, err
	}
	return data, nil
}

// Stat implements fs.StatFS interface.
func (s *BackupFS) Stat(name string) (fs.FileInfo, error) {
	stat, err := fs.Stat(s.fsys, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fs.Stat(s.backup, name)
		}
		return nil, err
	}
	return stat, nil
}

// Cleaned returns a channel that is closed when the backup directory is cleaned.
func (s *BackupFS) Cleaned() <-chan struct{} {
	return s.cleaned
}

// CleaningErr return the error when the backup is removed. The value is set only
// after the Cleaned() channel is closed.
func (s *BackupFS) CleaningErr() error {
	s.cleaningErrMu.Lock()
	defer s.cleaningErrMu.Unlock()
	return s.cleaningErr
}

func (s *BackupFS) copy(dir string) error {
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return fmt.Errorf("create backup data directory: %w", err)
	}

	return fs.WalkDir(s.fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		backupPath := filepath.Join(dir, path)
		if d.IsDir() {
			if err := os.MkdirAll(backupPath, 0o777); err != nil {
				return fmt.Errorf("create directory %s: %w", backupPath, err)
			}
			return nil
		}

		fr, err := s.fsys.Open(path)
		if err != nil {
			return fmt.Errorf("open file %s: %w", path, err)
		}
		defer fr.Close()

		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("file info %s: %w", path, err)
		}
		const permUserWrite = 0o200
		fw, err := os.OpenFile(backupPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm()|permUserWrite) // always user write
		if err != nil {
			return fmt.Errorf("create backup file %s: %w", backupPath, err)
		}
		defer fw.Close()

		if _, err := io.Copy(fw, fr); err != nil {
			return fmt.Errorf("copy file data %s: %w", backupPath, err)
		}
		return nil
	})
}

func uniqueStrings(s []string) []string {
	if len(s) <= 1 {
		return s
	}
	n := 1
	for i, x := range s[1:] {
		if x != s[i] {
			s[n] = x
			n++
		}
	}
	return s[:n]
}

func uniqueDirEntry(e []fs.DirEntry) []fs.DirEntry {
	if len(e) <= 1 {
		return e
	}
	n := 1
	for i, x := range e[1:] {
		if x.Name() != e[i].Name() {
			e[n] = x
			n++
		}
	}
	return e[:n]
}

func validateDir(dir string) bool {
	pathSeparator := string(os.PathSeparator)
	for _, n := range []string{
		"",
		".",
		"..",
		pathSeparator,
		"." + pathSeparator,
		pathSeparator + ".",
	} {
		if dir == n {
			return false
		}
	}
	return !strings.HasSuffix(dir, pathSeparator+"..")
}

type backupFile struct {
	name string
	fs.File
	backupFS fs.FS

	initialized bool
	isDir       bool
	backupFile  fs.ReadDirFile
}

func newBackupFile(name string, f fs.File, backupFS fs.FS) *backupFile {
	return &backupFile{
		name:     name,
		File:     f,
		backupFS: backupFS,
	}
}

// ReadDir reads the contents of the directory and returns
// a slice of up to n DirEntry values in directory order.
// Subsequent calls on the same file will yield further DirEntry values.
//
// If n > 0, ReadDir returns an error as not supported argument.
//
// If n <= 0, ReadDir returns all the DirEntry values from the directory
// in a single slice. In this case, if ReadDir succeeds (reads all the way
// to the end of the directory), it returns the slice and a nil error.
// If it encounters an error before the end of the directory,
// ReadDir returns the DirEntry list read until that point and a non-nil error.
func (f *backupFile) ReadDir(n int) ([]fs.DirEntry, error) {
	dir, ok := f.File.(fs.ReadDirFile)
	if !ok {
		return nil, &fs.PathError{Op: "readdir", Path: f.name, Err: errors.New("not implemented")}
	}

	if !f.initialized {
		s, err := f.File.Stat()
		if err != nil {
			return nil, err
		}
		f.isDir = s.IsDir()
		bf, err := f.backupFS.Open(f.name)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if dir, ok := bf.(fs.ReadDirFile); ok {
			f.backupFile = dir
		}
		f.initialized = true
	}

	if !f.isDir {
		return nil, errors.New("not a directory")
	}

	if n >= 0 {
		return nil, &fs.PathError{Op: "readdir", Path: f.name, Err: errors.New("BackupFS File does not support positive arguments for ReadDir")}
	}

	if f.backupFile == nil {
		return dir.ReadDir(n)
	}

	r, err := dir.ReadDir(n)
	if err != nil {
		return nil, err
	}
	rc, err := f.backupFile.ReadDir(n)
	if err != nil {
		return nil, err
	}
	r = append(r, rc...)
	sort.SliceStable(r, func(i, j int) bool {
		return r[i].Name() < r[j].Name()
	})
	return uniqueDirEntry(r), nil
}

func (f *backupFile) Close() error {
	if err := f.File.Close(); err != nil {
		return err
	}
	if f.backupFile != nil {
		return f.backupFile.Close()
	}
	return nil
}
