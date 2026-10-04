// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

// Package atomicfile writes a file all at once: a reader never sees it
// half-written, and a crash between two writers leaves the old file or the
// new one, never a torn mix. The data and the rename are flushed to disk
// before Write returns, so a power cut right after cannot leave an empty file
// where the old one was.
package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
)

// Write writes data to path via a unique temp file in the same directory,
// then renames it into place with perm.
func Write(path string, data []byte, perm os.FileMode) error {
	tmp, err := writeTemp(path, data, perm)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	syncDir(filepath.Dir(path))
	return nil
}

// ErrExists is what WriteNew returns when path is already there.
var ErrExists = errors.New("file already exists")

// WriteNew is Write for a file that must never be replaced once it exists
// (a key): ErrExists if path is already there, even if it appeared while the
// new one was being written.
func WriteNew(path string, data []byte, perm os.FileMode) error {
	if _, err := os.Lstat(path); err == nil {
		return ErrExists
	}
	tmp, err := writeTemp(path, data, perm)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	// A hard link fails if the name is taken: no window in which a second
	// writer's file replaces the first one's.
	if err := os.Link(tmp, path); err != nil {
		if _, serr := os.Lstat(path); serr == nil {
			return ErrExists
		}
		return err
	}
	syncDir(filepath.Dir(path))
	return nil
}

func writeTemp(path string, data []byte, perm os.FileMode) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".reminal-*.tmp")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	serr := f.Sync()
	cerr := f.Close()
	for _, e := range []error{werr, serr, cerr} {
		if e != nil {
			_ = os.Remove(tmp)
			return "", e
		}
	}
	if err := os.Chmod(tmp, perm); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// syncDir flushes a directory entry change (a rename, a new link). Not
// possible on Windows, where NTFS journals the rename itself.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}
