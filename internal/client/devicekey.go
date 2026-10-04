// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"reminal/internal/atomicfile"
	"reminal/internal/atrest"
)

// This device's owner key is the one file under ~/.reminal that grants
// standing access: it opens every machine this device owns until revoked. It
// is kept sealed (internal/atrest) in device_ed25519.sealed, so a backup, a
// synced folder or a disk image does not carry it in the clear.
//
// An identity key is not a saved session, and its failure rules differ on
// purpose. A saved session that cannot be opened is set aside and life goes
// on; an owner key that cannot be opened is reported, and NEVER replaced: a
// new key would make this device a stranger to every machine it owns, and
// the person would have to enrol it again on each one. Only `reminal own
// reset` makes a new identity, and it says what that costs.
//
// device_ed25519, where older versions kept the key in the clear, stays in
// place holding a sentinel line once the key is sealed. An older version run
// again finds a file it cannot parse and stops with its "key is corrupt; move
// it aside" error, instead of quietly minting a second identity.

const (
	deviceKeySealedName = "device_ed25519.sealed"
	deviceKeyKind       = "owner-key"
	deviceKeyID         = "device"
	// deviceKeySentinel is what device_ed25519 holds once the key is sealed.
	// It must never parse as a key: spaces keep it from being base64.
	deviceKeySentinel = "sealed: this device's owner key is kept encrypted in device_ed25519.sealed (reminal 3.15.12 or later)\n"
)

// ErrOwnerKeyLocked: the key exists but the keystore holding the key that
// seals it will not answer right now (a locked login keychain over SSH).
var ErrOwnerKeyLocked = errors.New("owner key is locked")

// ErrOwnerKeyUnreadable: the sealed key cannot be opened at all — its key is
// gone from the keystore, or the file is damaged. Nothing is replaced.
var ErrOwnerKeyUnreadable = errors.New("owner key cannot be read")

func deviceKeySealedPath() (string, error) {
	dir, err := reminalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, deviceKeySealedName), nil
}

// ownerKeyHint turns the two owner-key failures into one line a person can
// act on. Other errors pass through.
func ownerKeyHint(err error) error {
	switch {
	case errors.Is(err, ErrOwnerKeyLocked):
		if runtime.GOOS == "darwin" {
			return fmt.Errorf("%w: your login keychain is locked, so this device's owner key can't be read. Run `security unlock-keychain ~/Library/Keychains/login.keychain-db` and try again", ErrOwnerKeyLocked)
		}
		return fmt.Errorf("%w: the keyring holding this device's owner key isn't available right now (log in to the desktop, or unlock it) and try again", ErrOwnerKeyLocked)
	case errors.Is(err, ErrOwnerKeyUnreadable):
		return fmt.Errorf("%w: %s can't be opened, so this device can't prove it owns anything. Nothing was changed. `reminal own reset` makes a new identity, which you then enrol on each machine again with `sudo reminal add owner`", ErrOwnerKeyUnreadable, deviceKeySealedName)
	}
	return err
}

// loadOrCreateDeviceKey returns this DEVICE's Ed25519 private key, minting one
// on first use. The private key never leaves this device — only the public id
// (see MyOwnerID) is shared.
func loadOrCreateDeviceKey() (ed25519.PrivateKey, error) {
	k, err := loadDeviceKey()
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return k, ownerKeyHint(err)
	}
	keyMintMu.Lock()
	defer keyMintMu.Unlock()
	dir, err := reminalDir()
	if err != nil {
		return nil, err
	}
	unlock, err := atrest.Lock(dir, "device-key.lock", 5e9)
	if err != nil {
		return nil, err
	}
	defer unlock()
	// Whoever waited on the lock may be looking at a key the winner just
	// wrote; take that one rather than replacing it.
	if k, err := loadDeviceKey(); err == nil || !errors.Is(err, os.ErrNotExist) {
		return k, ownerKeyHint(err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := sealDeviceKey(priv); err != nil {
		// The keystore gave nothing to seal with (atrest already fell back
		// to its key file; this is rarer still). The identity must exist
		// from its first use, so it goes down the old way and is sealed at
		// the next use.
		plain, perr := deviceKeyPath()
		if perr != nil {
			return nil, perr
		}
		if werr := atomicfile.Write(plain, []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600); werr != nil {
			return nil, werr
		}
	}
	return priv, nil
}

// loadDeviceKey reads the key as it is on disk: the sealed copy first, else a
// plain one an older version wrote (sealed on the way, when it can be).
// os.ErrNotExist when there is none at all.
func loadDeviceKey() (ed25519.PrivateKey, error) {
	sp, err := deviceKeySealedPath()
	if err != nil {
		return nil, err
	}
	plain, err := deviceKeyPath()
	if err != nil {
		return nil, err
	}
	blob, serr := os.ReadFile(sp)
	if serr == nil {
		pt, err := atrest.Open(deviceKeyKind, deviceKeyID, blob)
		switch {
		case err == nil:
			if len(pt) != ed25519.PrivateKeySize {
				return nil, ErrOwnerKeyUnreadable
			}
			// A plain copy still there (a crash between sealing and the
			// sentinel) is replaced by the sentinel now.
			if b, err := os.ReadFile(plain); err == nil && !isDeviceKeySentinel(b) {
				_ = atomicfile.Write(plain, []byte(deviceKeySentinel), 0o600)
			}
			return ed25519.PrivateKey(pt), nil
		case errors.Is(err, atrest.ErrLocked):
			return nil, ErrOwnerKeyLocked
		default:
			return nil, ErrOwnerKeyUnreadable
		}
	}
	if !errors.Is(serr, os.ErrNotExist) {
		return nil, serr
	}
	b, perr := os.ReadFile(plain)
	if perr != nil {
		return nil, perr // os.ErrNotExist: no key at all
	}
	if isDeviceKeySentinel(b) {
		// The sealed copy this points at is gone.
		return nil, ErrOwnerKeyUnreadable
	}
	raw, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if derr != nil || len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("key at %s is corrupt; move it aside to mint a new identity", plain)
	}
	priv := ed25519.PrivateKey(raw)
	// An older version's plain key: seal it now. If that fails (nothing to
	// seal with yet) the plain copy stays and we try again next time.
	_ = sealDeviceKey(priv)
	return priv, nil
}

// sealDeviceKey writes the sealed copy, proves it opens, then leaves the
// sentinel where the plain key was. The plain copy is never removed before
// the sealed one has been read back.
func sealDeviceKey(priv ed25519.PrivateKey) error {
	sp, err := deviceKeySealedPath()
	if err != nil {
		return err
	}
	plain, err := deviceKeyPath()
	if err != nil {
		return err
	}
	blob, err := atrest.Seal(deviceKeyKind, deviceKeyID, priv)
	if err != nil {
		return err
	}
	if err := atomicfile.Write(sp, blob, 0o600); err != nil {
		return err
	}
	back, err := os.ReadFile(sp)
	if err != nil {
		return err
	}
	if pt, err := atrest.Open(deviceKeyKind, deviceKeyID, back); err != nil || string(pt) != string(priv) {
		return fmt.Errorf("sealed owner key does not read back")
	}
	return atomicfile.Write(plain, []byte(deviceKeySentinel), 0o600)
}

func isDeviceKeySentinel(b []byte) bool {
	return strings.HasPrefix(strings.TrimSpace(string(b)), "sealed:")
}

// HasDeviceKey reports whether this device already has an identity key (the user
// has set it up as a potential owner), WITHOUT minting one. Used to decide
// whether to try a PIN-free connect before falling back to asking for a PIN.
func HasDeviceKey() bool {
	if sp, err := deviceKeySealedPath(); err == nil {
		if _, err := os.Stat(sp); err == nil {
			return true
		}
	}
	path, err := deviceKeyPath()
	if err != nil {
		return false
	}
	b, err := os.ReadFile(path)
	return err == nil && !isDeviceKeySentinel(b)
}

// OwnerKeyState describes the owner key for `reminal doctor`: "none",
// "sealed", "plain" (an older version's file, not yet sealed), "locked" or
// "unreadable". It only looks; a check writes nothing.
func OwnerKeyState() string {
	sp, err := deviceKeySealedPath()
	if err != nil {
		return "unreadable"
	}
	plain, err := deviceKeyPath()
	if err != nil {
		return "unreadable"
	}
	if blob, err := os.ReadFile(sp); err == nil {
		_, err := atrest.Open(deviceKeyKind, deviceKeyID, blob)
		switch {
		case err == nil:
			return "sealed"
		case errors.Is(err, atrest.ErrLocked):
			return "locked"
		}
		return "unreadable"
	}
	b, err := os.ReadFile(plain)
	if err != nil {
		return "none"
	}
	if isDeviceKeySentinel(b) {
		return "unreadable"
	}
	if _, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b))); err != nil {
		return "unreadable"
	}
	return "plain"
}

// ResetDeviceKey throws this device's owner identity away and makes a new one.
// Every machine that knew the old one must be told the new id (`reminal own`,
// then `sudo reminal add owner` there). The caller confirms with the person.
func ResetDeviceKey() (newID string, err error) {
	keyMintMu.Lock()
	for _, f := range []func() (string, error){deviceKeySealedPath, deviceKeyPath} {
		if p, err := f(); err == nil {
			_ = os.Remove(p)
		}
	}
	keyMintMu.Unlock()
	priv, err := loadOrCreateDeviceKey()
	if err != nil {
		return "", err
	}
	return ownerID(priv.Public().(ed25519.PublicKey)), nil
}
