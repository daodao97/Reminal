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
// not. Caller holds keyMu. Cheap: a stat for the key file; a lookup at most
// once a minute for an OS keystore.
func confirmCurrent(dir string, m *meta, id [idLen]byte) {
	k := keys[dir][id]
	if k == nil || hex.EncodeToString(id[:]) != m.ID {
		return
	}
	st := storeAt(dir, m)
	if st == nil {
		return
	}
	isOS := st.source() != srcFile
	if isOS {
		if time.Since(lastConfirm[dir]) < confirmEvery || osLocked() {
			return
		}
		lastConfirm[dir] = time.Now()
	} else if _, err := os.Lstat(fileStore{dir: dir}.path()); err == nil {
		return
	}
	if isOS {
		got, err := st.get()
		if err != nil || len(got) == keyLen { // locked, or still there
			return
		}
		// Answered "not found" (confirmed by the canary): put it back.
	}
	if err := checkWritable(dir); err != nil {
		return
	}
	if err := st.put(k); err != nil {
		return
	}
	if got, err := st.get(); err == nil && string(got) == string(k) && !healed {
		healed = true
		Logf("reminal: the at-rest key was missing from %s and has been written back from memory", storeName(st, dir))
	}
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
	id, ok := current[dir]
	if !ok || hex.EncodeToString(id[:]) != m.ID {
		return ""
	}
	return hex.EncodeToString(keys[dir][id])
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
	Source  string // "", "file", "keychain", "dpapi", "secret-service"
	Missing bool   // atrest.json names a key its store does not hold (definitive for the key file)
	// FileOnDesktop: the key is a file although this is a real login user on
	// macOS or Windows — it was minted outside the GUI keystore (over SSH,
	// or by a test binary) and the daemon moves it in once the keystore
	// answers.
	FileOnDesktop bool
}

// Status reports the sealing key's state without writing anything.
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
		return s
	}
	s.Source = m.Source
	if m.Source == "file" {
		if _, err := os.Lstat(fileStore{dir: dir}.path()); err != nil {
			s.Missing = true
		}
		if osStoreUsable() && osStoreFor(dir, "") != nil && hasDesktopKeystore {
			s.FileOnDesktop = true
		}
	}
	return s
}
