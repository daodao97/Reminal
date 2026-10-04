// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package atrest

import (
	"encoding/hex"
	"fmt"
	"strings"
)

const caseInsensitiveFS = true

// keychainStore keeps the key as a generic password in the login Keychain,
// through /usr/bin/security. Builds are cgo-free, so reminal cannot call the
// Security framework itself; the item's access list therefore trusts the
// `security` tool, which means any process running as this user can read it
// without a prompt — no weaker than a 0600 file, and the key stays out of
// backups and disk images. The login Keychain is a file-based keychain and is
// never synced to iCloud.
type keychainStore struct{ account string }

func osStore(dir string) store { return keychainStore{account: keystoreAccount(dir)} }

func (keychainStore) name() string { return "keychain" }
func (keychainStore) source() byte { return srcKeychain }

// errSecItemNotFound, as `security` reports it in its exit status.
const secItemNotFound = 44

func (s keychainStore) get() ([]byte, error) {
	out, _, code, err := runTool(nil, "/usr/bin/security", "find-generic-password",
		"-s", keystoreService, "-a", s.account, "-w")
	if code == secItemNotFound {
		return nil, errNotFound
	}
	if err != nil {
		return nil, ErrLocked
	}
	k, err := hex.DecodeString(strings.TrimSpace(string(out)))
	if err != nil || len(k) != keyLen {
		return nil, ErrLocked
	}
	return k, nil
}

// put adds the item. The secret goes in on stdin (`security -i`), never in an
// argv another process could read.
func (s keychainStore) put(k []byte) error {
	cmd := fmt.Sprintf("add-generic-password -U -s %s -a %s -w %s\n",
		keystoreService, s.account, hex.EncodeToString(k))
	_, stderr, _, err := runTool([]byte(cmd), "/usr/bin/security", "-i")
	if err != nil {
		return err
	}
	// `security -i` exits 0 even when a command in it failed.
	if strings.Contains(string(stderr), "security:") || strings.Contains(string(stderr), "rror") {
		return fmt.Errorf("keychain: %s", strings.TrimSpace(string(stderr)))
	}
	return nil
}
