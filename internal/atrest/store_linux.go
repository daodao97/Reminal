// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package atrest

import (
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const caseInsensitiveFS = false

// secretServiceStore keeps the key in the desktop keyring (GNOME Keyring,
// KWallet) through `secret-tool`. Only offered when the tool is installed and
// there is a session bus to reach it on; a headless box uses the file.
type secretServiceStore struct{ account string }

func osStore(dir string) store {
	if _, err := exec.LookPath("secret-tool"); err != nil {
		return nil
	}
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		rt := os.Getenv("XDG_RUNTIME_DIR")
		if rt == "" {
			return nil
		}
		if _, err := os.Stat(filepath.Join(rt, "bus")); err != nil {
			return nil
		}
	}
	return secretServiceStore{account: keystoreAccount(dir)}
}

func (secretServiceStore) name() string { return "secret-service" }
func (secretServiceStore) source() byte { return srcSecretSvc }

func (s secretServiceStore) get() ([]byte, error) {
	out, stderr, code, err := runTool(nil, "secret-tool", "lookup",
		"service", keystoreService, "account", s.account)
	// "Nothing stored" is exit 1 with nothing said; a bus or unlock failure
	// says why on stderr.
	if code == 1 && len(strings.TrimSpace(string(out))) == 0 && len(strings.TrimSpace(string(stderr))) == 0 {
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

func (s secretServiceStore) put(k []byte) error {
	_, _, _, err := runTool([]byte(hex.EncodeToString(k)), "secret-tool", "store",
		"--label=reminal at-rest key", "service", keystoreService, "account", s.account)
	return err
}
