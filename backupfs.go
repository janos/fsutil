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

// BackupFS implements a filesystem which copies all data from another filesystem
// to a directory when it is constructed. It uses it as a backup for a given
// timeout value in case files in the original filesystem change. The intended
// usage is with embedded filesystems to temporary preserve older files if they
// are needed shorty after new embedded filesystem with new files is available.
type BackupFS struct {
	fsys          fs.FS
	backup        fs.FS
	cleaned       chan struct{}
	cleaningErr   error
	cleaningErrMu sync.Mutex
}

// NewBackupFS constructs a new BackupFS for another filesystem, that is copied in
// dir with the backup timeout.
func NewBackupFS(fsys fs.FS, dir string, timeout time.Duration) (*BackupFS, error) {
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
		t := time.NewTimer(timeout)
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
			return s.backup.Open(name)
		}
		return nil, err
	}
	return f, nil
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
