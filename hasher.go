// Copyright (c) 2021, Janoš Guljaš <janos@resenje.org>
// All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fsutil

import (
	"crypto/md5"
	"encoding/hex"
	"io"
)

var hexChars = []rune("0123456789abcdef")

// Hasher defines an interface for hashing file paths.
type Hasher interface {
	Hash(io.Reader) (string, error)
	IsHash(string) bool
}

// MD5Hasher uses MD5 sum to compute a file hash.
type MD5Hasher struct {
	hashLength int
}

// NewMD5Hasher creates a new instance of MD5Hasher.
func NewMD5Hasher(hashLength int) *MD5Hasher {
	return &MD5Hasher{
		hashLength: hashLength,
	}
}

// Hash returns a part of a MD5 sum of a file.
func (s *MD5Hasher) Hash(reader io.Reader) (string, error) {
	hash := md5.New()
	if _, err := io.Copy(hash, reader); err != nil {
		return "", err
	}
	h := hash.Sum(nil)
	if len(h) < s.hashLength {
		return "", nil
	}
	return hex.EncodeToString(h)[:s.hashLength], nil
}

// IsHash checks is provided string a valid hash.
func (s *MD5Hasher) IsHash(h string) bool {
	if len(h) != s.hashLength {
		return false
	}
	var found bool
	for _, c := range h {
		found = false
		for _, m := range hexChars {
			if c == m {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
