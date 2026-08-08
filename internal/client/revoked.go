// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// revokedMu serialises the tombstone store's read-modify-write. The directory
// host handles each dir_revoke_self in its own goroutine, so without this two
// concurrent self-revokes would both read the file, each add only their own
// entry, and the second write would clobber the first — silently dropping a
// revocation. All directory ops for a machine run in the single elected host
// process, so a process-local mutex is sufficient.
var revokedMu sync.Mutex

// revoked_owners.json is this machine's list of owner devices that have
// self-revoked their PIN-free access (the Machines panel's ✕). It lives in the
// AGENT-writable ~/.reminal — NOT the root-owned, sudo-gated owners.json —
// because a self-revoke is benign (a device only ever locks ITSELF out) and so
// must not require host presence, unlike enrolling an owner (which grants access
// and stays sudo-gated). IsOwner treats a device as an owner only if it's in
// owners.json AND not tombstoned here.
//
// Per-user: it governs the sessions this user's agents serve — complete
// revocation on the common single-user machine. `reminal owners restore` clears
// a tombstone to re-enable an enrolled-but-self-revoked device.

func revokedPath() (string, error) {
	dir, err := reminalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "revoked_owners.json"), nil
}

func loadRevoked() (map[string]bool, error) {
	path, err := revokedPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return map[string]bool{}, nil
	}
	var ids []string
	if err := json.Unmarshal(b, &ids); err != nil {
		return nil, fmt.Errorf("revoked_owners.json is corrupt: %w", err)
	}
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m, nil
}

func saveRevoked(m map[string]bool) error {
	dir, err := reminalDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path, err := revokedPath()
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	b, err := json.MarshalIndent(ids, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(b, '\n'), 0o600)
}

// RevokeSelf tombstones a device's ownership on this machine. Idempotent.
func RevokeSelf(pub ed25519.PublicKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("device key must be %d bytes", ed25519.PublicKeySize)
	}
	revokedMu.Lock()
	defer revokedMu.Unlock()
	m, err := loadRevoked()
	if err != nil {
		return err
	}
	m[ownerID(pub)] = true
	return saveRevoked(m)
}

// IsRevoked reports whether a device's ownership has been tombstoned.
func IsRevoked(pub ed25519.PublicKey) (bool, error) {
	m, err := loadRevoked()
	if err != nil {
		return false, err
	}
	return m[ownerID(pub)], nil
}

// RevokedIDs returns the set of owner ids (rmnl_…) currently tombstoned, so the
// CLI can mark them in `reminal owners`.
func RevokedIDs() (map[string]bool, error) {
	return loadRevoked()
}

// RestoreOwnerTarget clears the tombstone of the owner matched by id/label,
// re-enabling an enrolled-but-self-revoked device. The int is the match count
// (0 none, 1 matched, >1 ambiguous) like RemoveOwner; wasRevoked reports whether
// a tombstone was actually cleared (false → the device was already active).
func RestoreOwnerTarget(target string) (o Owner, n int, wasRevoked bool, err error) {
	of, err := loadOwners()
	if err != nil {
		return Owner{}, 0, false, err
	}
	m := matchOwners(of, target)
	if len(m) != 1 {
		return Owner{}, len(m), false, nil
	}
	o = of.Owners[m[0]]
	pub, err := parseOwnerID(o.Pubkey)
	if err != nil {
		return Owner{}, 0, false, err
	}
	cleared, err := RestoreOwner(pub)
	if err != nil {
		return Owner{}, 0, false, err
	}
	return o, 1, cleared, nil
}

// RestoreOwner clears a device's tombstone, re-enabling an enrolled-but-self-
// revoked owner. Returns whether a tombstone was actually cleared.
func RestoreOwner(pub ed25519.PublicKey) (bool, error) {
	revokedMu.Lock()
	defer revokedMu.Unlock()
	m, err := loadRevoked()
	if err != nil {
		return false, err
	}
	id := ownerID(pub)
	if !m[id] {
		return false, nil
	}
	delete(m, id)
	return true, saveRevoked(m)
}
