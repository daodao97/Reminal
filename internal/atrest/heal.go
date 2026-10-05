// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package atrest

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The key atrest.json names as current is what every running agent and the
// daemon hold in memory and save with. Losing the FILE that holds it (or the
// keystore entry) while they run is damage, not rotation: nothing sealed
// under it may be called gone, and whoever still has the key in memory puts
// it back. On 2026-10-04 a deleted atrest.key sat unnoticed under 49 records
// while eleven processes each held the key that would have fixed it.

// ErrCurrentKeyMissing: atrest.json names a key its store no longer holds,
// and this process does not have it in memory. Saved files stay as they are
// (they open again once the key is back); `reminal doctor --repair-key` asks
// a running session for it.
var ErrCurrentKeyMissing = fmt.Errorf("%w: the at-rest key this machine saves with is missing from its store", ErrLocked)

// errTestRealHome: a test binary reached the real home.
var errTestRealHome = errors.New("atrest: refusing to write under the real home from a test binary; redirect HOME")

// confirmEvery bounds how often an OS keystore is asked whether it still
// holds the current key; the key file is a stat, checked every time.
const confirmEvery = time.Minute

var (
	lastConfirm = map[string]time.Time{}
	healed      bool
)

// realHome is the login user's home, where a test binary must never write.
// A var so a test can point it at a scratch dir and prove the refusal
// without going anywhere near the person's own files.
var realHome = func() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.HomeDir
}

// checkWritable refuses, from a test binary, any write under the real home's
// ~/.reminal: a test that forgets to redirect HOME must fail loudly, never
// mint the key the person's own daemon then adopts.
func checkWritable(dir string) error {
	if !testing.Testing() {
		return nil
	}
	home := realHome()
	if home == "" {
		return nil
	}
	real := canonical(filepath.Join(home, ".reminal"))
	d := canonical(dir)
	if d == real || strings.HasPrefix(d, real+string(filepath.Separator)) {
		return errTestRealHome
	}
	return nil
}

// CheckWritable is checkWritable for other packages that write under
// ~/.reminal (session records, the owner key).
func CheckWritable() error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	return checkWritable(dir)
}

// confirmCurrent makes sure the store atrest.json names still holds the
// current key this process has in memory, and writes it back when it does
// not. False when this key must not be used any more: the store now holds a
// different key (another process rotated) or the write-back lost such a
// race; the caller then re-reads atrest.json and adopts what is current.
// Caller holds keyMu. Cheap: a stat for the key file; a lookup at most once
// a minute for an OS keystore, and none while it is on the back-off list.
func confirmCurrent(dir string, m *meta, id [idLen]byte) bool {
	k := keys[dir][id]
	if k == nil || hex.EncodeToString(id[:]) != m.ID {
		return false
	}
	st := storeAt(dir, m)
	if st == nil {
		return true
	}
	if st.source() != srcFile {
		if time.Since(lastConfirm[dir]) < confirmEvery || osLocked() {
			return true
		}
		lastConfirm[dir] = time.Now()
		got, err := st.get()
		if err == nil {
			return string(got) == string(k) // another key there means ours is stale
		}
		if !errors.Is(err, errNotFound) {
			lockedUntil = time.Now().Add(lockedBackoff) // locked or unreachable: no more asking for a while
			return true
		}
	} else if _, err := os.Lstat(fileStore{dir: dir}.path()); err == nil {
		return true
	}
	return healCurrent(dir, id, k)
}

// healCurrent writes the current key back into its store — create-only, under
// the atrest lock, and only while atrest.json still names it: between a
// holder's "not found" and its write, another process may have minted a new
// key and named it, and a stale key written over that one would orphan
// everything sealed since. Caller holds keyMu. True when the key is in place
// afterwards, by us or by someone else.
func healCurrent(dir string, id [idLen]byte, k []byte) bool {
	if err := checkWritable(dir); err != nil {
		return false
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return false
	}
	defer unlock()
	m, err := readMeta(dir)
	if err != nil || hex.EncodeToString(id[:]) != m.ID {
		return false // no longer the current key: not ours to put back
	}
	st := storeAt(dir, m)
	if st == nil {
		return false
	}
	switch err := st.putNew(k); {
	case err == nil:
	case errors.Is(err, errExists):
		// Someone else got there first; whatever is there wins.
	default:
		return false
	}
	got, err := st.get()
	if err != nil || string(got) != string(k) {
		return false
	}
	if m2, err := readMeta(dir); err != nil || m2.ID != m.ID {
		return false
	}
	if !healed {
		healed = true
		Logf("reminal: the at-rest key was missing from %s and has been written back from memory", storeName(st, dir))
	}
	return true
}

func storeName(st store, dir string) string {
	if st.source() == srcFile {
		return fileStore{dir: dir}.path()
	}
	return "the " + st.name()
}

// CurrentKeyHex returns the current key as this process holds it in memory,
// for `reminal doctor --repair-key` over a same-user control socket, or ""
// when this process has none that matches atrest.json.
func CurrentKeyHex() string {
	dir, err := Dir()
	if err != nil {
		return ""
	}
	keyMu.Lock()
	defer keyMu.Unlock()
	m, err := readMeta(dir)
	if err != nil {
		return ""
	}
	for id, k := range keys[dir] {
		if hex.EncodeToString(id[:]) == m.ID {
			return hex.EncodeToString(k) // held: sealed with it, or opened with it
		}
	}
	return ""
}

// RestoreKey writes a key recovered from a running process back into the
// store atrest.json names — only if it IS the key atrest.json names.
func RestoreKey(keyHex string) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := checkWritable(dir); err != nil {
		return err
	}
	k, err := hex.DecodeString(keyHex)
	if err != nil || len(k) != keyLen {
		return errors.New("that is not an at-rest key")
	}
	keyMu.Lock()
	defer keyMu.Unlock()
	m, err := readMeta(dir)
	if err != nil {
		return fmt.Errorf("atrest.json: %w", err)
	}
	id := keyID(k)
	if hex.EncodeToString(id[:]) != m.ID {
		return errors.New("that key is not the one atrest.json names; nothing written")
	}
	st := storeAt(dir, m)
	if st == nil {
		return ErrLocked
	}
	if got, err := st.get(); err == nil && string(got) == string(k) {
		remember(dir, k)
		current[dir] = id
		return nil // already there
	}
	if err := st.put(k); err != nil {
		return err
	}
	got, err := st.get()
	if err != nil || string(got) != string(k) {
		return errors.New("the key did not read back after writing")
	}
	remember(dir, k)
	current[dir] = id
	return nil
}

// KeyStatus is what `reminal doctor` says about the sealing key.
type KeyStatus struct {
	Source string // "", "file", "keychain", "dpapi", "secret-service"
	// Missing: atrest.json names a key its store does not hold. Definitive
	// for stores that are files (the key file, the DPAPI blob).
	Missing bool
	// MaybeMissing: a keychain or keyring answered "not found" to a lookup
	// that, being a check, wrote no canary to confirm it; it could also be a
	// keychain outside this login session.
	MaybeMissing bool
	// MetaDamaged: atrest.json is there but cannot be read.
	MetaDamaged bool
	// FileOnDesktop: the key is a file although this is a real login user on
	// macOS or Windows — it was minted outside the GUI keystore (over SSH,
	// or by a test binary) and the daemon moves it in once the keystore
	// answers.
	FileOnDesktop bool
}

// KeyState reports the sealing key's state without writing anything.
func KeyState() KeyStatus {
	var s KeyStatus
	dir, err := Dir()
	if err != nil {
		return s
	}
	keyMu.Lock()
	defer keyMu.Unlock()
	m, err := readMeta(dir)
	if err != nil {
		s.MetaDamaged = err != nil && !errors.Is(err, os.ErrNotExist)
		return s
	}
	s.Source = m.Source
	switch m.Source {
	case "file":
		if _, err := os.Lstat(fileStore{dir: dir}.path()); err != nil {
			s.Missing = true
		}
		if osStoreUsable() && osStoreFor(dir, "") != nil && hasDesktopKeystore {
			s.FileOnDesktop = true
		}
	case "dpapi":
		if st, ok := storeAt(dir, m).(interface{ path() string }); ok {
			if _, err := os.Lstat(st.path()); err != nil {
				s.Missing = true
			}
		}
	default: // keychain, secret-service: a lookup, no canary
		st := storeAt(dir, m)
		if st == nil || osLocked() {
			return s
		}
		canaryOn = false
		was := lockedUntil
		_, err := st.get()
		canaryOn = true
		lockedUntil = was
		if errors.Is(err, ErrLocked) || errors.Is(err, errNotFound) {
			// Without the canary a "not found" reads as locked, and a locked
			// keystore cannot be told from a deleted entry here; both say
			// "can't be found right now", and a repair is harmless either way.
			s.MaybeMissing = true
		}
	}
	return s
}

// MissingKeyLikely is Missing or MaybeMissing: enough to try a repair.
func (s KeyStatus) MissingKeyLikely() bool { return s.Missing || s.MaybeMissing }
