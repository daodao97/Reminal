// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// An upload's "auto-delete in 1h" is a promise made to someone who is about to
// close the session — a photo sent from a phone, pasted into a command, done
// with. The timer that kept that promise lived in the session's own process,
// so ending the session (or a restart, an upgrade, a reboot) cancelled the
// deletion and left the file on the host for good. The person was told it
// would go; nothing was ever going to remove it.
//
// So the deadline is written down instead of only being remembered: the
// session records it, the daemon sweeps it, and any agent starting up sweeps
// it too, so a machine with no daemon still catches up. The in-process timer
// stays — it is what makes deletion prompt while the session is alive — and
// the record is what survives the process.

// uploadTTLFile is the record, beside the rest of a machine's reminal state.
const uploadTTLFile = "uploads.json"

// uploadTTLLock serialises read-modify-write across the sessions and the
// daemon, which all share one record.
const uploadTTLLock = "uploads.lock"

// uploadSweepEvery is how often the daemon looks. A minute is far below any
// TTL a viewer offers, and the sweep is a stat per entry on a list that is
// normally empty.
const uploadSweepEvery = time.Minute

// uploadTTLEntry is one uploaded file and when it stops being wanted.
type uploadTTLEntry struct {
	Path     string    `json:"path"`
	DeleteAt time.Time `json:"delete_at"`
}

func uploadTTLPath() (string, error) {
	dir, err := reminalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, uploadTTLFile), nil
}

// uploadsDir is the one directory an entry may name. Deleting files is not
// something to do on the strength of a path in a file: a record that is
// corrupt, stale, or written by something else must not be able to remove
// anything a person did not upload.
func uploadsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Downloads", "reminal"), nil
}

// deletableUpload reports whether this entry names a file the sweeper may
// remove: a regular file, directly inside the uploads directory. Symlinks are
// refused — following one would delete its target somewhere else entirely.
func deletableUpload(path string) bool {
	dir, err := uploadsDir()
	if err != nil {
		return false
	}
	clean := filepath.Clean(path)
	if filepath.Dir(clean) != filepath.Clean(dir) {
		return false
	}
	st, err := os.Lstat(clean)
	if err != nil {
		return false
	}
	return st.Mode().IsRegular()
}

// readUploadTTLs reads the record. A missing or unreadable one is empty, not
// an error: the record is a convenience for the sweeper, never the thing that
// decides whether uploading works.
func readUploadTTLs() []uploadTTLEntry {
	p, err := uploadTTLPath()
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var out []uploadTTLEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

// writeUploadTTLs replaces the record, atomically, so a sweep interrupted
// halfway cannot leave a half-written list behind.
func writeUploadTTLs(entries []uploadTTLEntry) error {
	p, err := uploadTTLPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	if len(entries) == 0 {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].DeleteAt.Before(entries[j].DeleteAt) })
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// withUploadTTLs runs fn against the current record and saves what it returns,
// with the lock held so two sessions finishing an upload at once cannot lose
// one another's entry. A lock that cannot be taken is not fatal: the worst
// case is a lost entry, and refusing to record anything would be worse.
func withUploadTTLs(fn func([]uploadTTLEntry) []uploadTTLEntry) error {
	if lock, held, err := tryLockFile(uploadTTLLock); err == nil && held {
		defer unlockFile(lock)
	}
	return writeUploadTTLs(fn(readUploadTTLs()))
}

// rememberUploadTTL writes down that this file should be gone by then.
func rememberUploadTTL(path string, deleteAt time.Time) {
	_ = withUploadTTLs(func(cur []uploadTTLEntry) []uploadTTLEntry {
		out := make([]uploadTTLEntry, 0, len(cur)+1)
		for _, e := range cur {
			if e.Path != path {
				out = append(out, e)
			}
		}
		return append(out, uploadTTLEntry{Path: path, DeleteAt: deleteAt})
	})
}

// forgetUploadTTL drops a file from the record — it has been deleted, by the
// session's own timer or by someone clearing out their Downloads.
func forgetUploadTTL(path string) {
	_ = withUploadTTLs(func(cur []uploadTTLEntry) []uploadTTLEntry {
		out := cur[:0]
		for _, e := range cur {
			if e.Path != path {
				out = append(out, e)
			}
		}
		return out
	})
}

// sweepUploadTTLs deletes every upload whose time is up and prunes entries for
// files that are already gone. Returns how many files it deleted.
func sweepUploadTTLs(now time.Time) int {
	deleted := 0
	_ = withUploadTTLs(func(cur []uploadTTLEntry) []uploadTTLEntry {
		keep := make([]uploadTTLEntry, 0, len(cur))
		for _, e := range cur {
			if _, err := os.Lstat(e.Path); errors.Is(err, os.ErrNotExist) {
				continue // already gone: nothing owed
			}
			if e.DeleteAt.After(now) {
				keep = append(keep, e)
				continue
			}
			if !deletableUpload(e.Path) {
				continue // not ours to delete; drop it rather than keep trying
			}
			if err := os.Remove(e.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
				keep = append(keep, e) // locked or read-only: try again next sweep
				continue
			}
			deleted++
		}
		return keep
	})
	return deleted
}

// sweepUploadsLoop keeps the promise for as long as the daemon runs. Started
// by the daemon, and run once by every agent at startup so a machine with no
// daemon still catches up the moment anything reminal runs.
func sweepUploadsLoop(stop <-chan struct{}) {
	sweepUploadTTLs(time.Now())
	t := time.NewTicker(uploadSweepEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case now := <-t.C:
			sweepUploadTTLs(now)
		}
	}
}
