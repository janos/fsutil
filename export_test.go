// Copyright (c) 2021, Janoš Guljaš <janos@resenje.org>
// All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fsutil

import "io/fs"

var (
	UniqueStrings  = uniqueStrings
	UniqueDirEntry = uniqueDirEntry
)

func NewDirEntry(e fs.DirEntry, name string) fs.DirEntry {
	return &dirEntry{e: e, name: name}
}

func NewFileInfo(i fs.FileInfo, name string) fs.FileInfo {
	return &fileInfo{i: i, name: name}
}
