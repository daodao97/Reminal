// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package atrest

import (
	"encoding/hex"
	"errors"
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

func osStore(dir, account string) store {
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
	return secretServiceStore{account: account}
}

func (secretServiceStore) name() string { return "secret-service" }
func (secretServiceStore) source() byte { return srcSecretSvc }

func (s secretServiceStore) get() ([]byte, error) {
	out, stderr, code, err := runTool(nil, "secret-tool", "lookup",
		"service", keystoreService, "account", s.account)
	// "Nothing stored" is exit 1 with nothing said; a bus or unlock failure
	// says why on stderr.
	// So is a locked collection whose unlock prompt was dismissed: believe
	// "not there" only once a throwaway item can be stored and read back.
	if code == 1 && len(strings.TrimSpace(string(out))) == 0 && len(strings.TrimSpace(string(stderr))) == 0 {
		if canaryOn && s.canary() {
			return nil, errNotFound
		}
		return nil, ErrLocked
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

func (s secretServiceStore) canary() bool {
	if _, _, _, err := runTool([]byte("01"), "secret-tool", "store",
		"--label=reminal at-rest check", "service", keystoreCanary, "account", s.account); err != nil {
		return false
	}
	out, _, _, err := runTool(nil, "secret-tool", "lookup", "service", keystoreCanary, "account", s.account)
	_, _, _, _ = runTool(nil, "secret-tool", "clear", "service", keystoreCanary, "account", s.account)
	return err == nil && strings.TrimSpace(string(out)) == "01"
}

func (s secretServiceStore) put(k []byte) error {
	_, _, _, err := runTool([]byte(hex.EncodeToString(k)), "secret-tool", "store",
		"--label=reminal at-rest key", "service", keystoreService, "account", s.account)
	return err
}

// putNew: secret-tool has no create-only store, so look first. Callers hold
// the atrest lock, which keeps reminal's own processes from racing here.
func (s secretServiceStore) putNew(k []byte) error {
	if got, err := s.get(); err == nil && len(got) == keyLen {
		return errExists
	} else if err != nil && !errors.Is(err, errNotFound) {
		return err
	}
	return s.put(k)
}

// hasDesktopKeystore: a real login user here is expected to have an OS keystore.
const hasDesktopKeystore = false
