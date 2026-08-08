// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// known_machines.json is this device's trust-on-first-connect record of the
// machine identity key it saw for a session — the client half of mutual auth.
// On first owner connect the device pins the machine's key here; on every later
// connect it verifies the machine presents the SAME key, so a relay can't
// impersonate the host. (Keyed by session id today; re-keys to a stable machine
// name once reach-by-name lands.) Same shape as SSH's known_hosts.

func knownMachinesPath() (string, error) {
	dir, err := reminalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "known_machines.json"), nil
}

func loadKnownMachines() (map[string]string, error) {
	path, err := knownMachinesPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return map[string]string{}, nil
	}
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("known_machines.json is corrupt: %w", err)
	}
	return m, nil
}

func saveKnownMachines(m map[string]string) error {
	dir, err := reminalDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path, err := knownMachinesPath()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(b, '\n'), 0o600)
}

// PinnedMachineKey returns the machine key previously pinned for sessionID and
// whether one exists.
func PinnedMachineKey(sessionID string) (ed25519.PublicKey, bool, error) {
	m, err := loadKnownMachines()
	if err != nil {
		return nil, false, err
	}
	enc, ok := m[sessionID]
	if !ok {
		return nil, false, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, false, nil // treat a corrupt entry as unpinned
	}
	return ed25519.PublicKey(raw), true, nil
}

// RecordMachineKey pins pub for sessionID on first sight. If a DIFFERENT key is
// already pinned it returns conflict=true and changes nothing — a possible relay
// MITM the caller MUST refuse. Pinning the same key again is a no-op.
func RecordMachineKey(sessionID string, pub ed25519.PublicKey) (conflict bool, err error) {
	if len(pub) != ed25519.PublicKeySize {
		return false, fmt.Errorf("machine key must be %d bytes, got %d", ed25519.PublicKeySize, len(pub))
	}
	m, err := loadKnownMachines()
	if err != nil {
		return false, err
	}
	enc := base64.RawURLEncoding.EncodeToString(pub)
	if cur, ok := m[sessionID]; ok {
		return cur != enc, nil
	}
	m[sessionID] = enc
	return false, saveKnownMachines(m)
}
