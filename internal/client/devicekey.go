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
	"time"

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
//
// Both files are read before anything is decided, and nothing on disk is
// written over unless the two agree: a valid plain key is never replaced by a
// sentinel for a sealed key that differs from it, and a damaged sealed file
// never hides a valid plain key.

const (
	deviceKeySealedName = "device_ed25519.sealed"
	deviceKeyKind       = "owner-key"
	deviceKeyID         = "device"
	deviceKeyLock       = "device-key.lock"
	deviceKeyLockWait   = 5 * time.Second
	// deviceKeySentinel is what device_ed25519 holds once the key is sealed.
	// It must never parse as a key: spaces keep it from being base64.
	deviceKeySentinel = "sealed: this device's owner key is kept encrypted in device_ed25519.sealed (reminal 3.15.12 or later)\n"
)

// ErrOwnerKeyLocked: the key exists but the keystore holding the key that
// seals it will not answer right now (a locked login keychain over SSH).
var ErrOwnerKeyLocked = errors.New("owner key is locked")

// ErrOwnerKeyUnreadable: the sealed key cannot be opened at all — its key is
// gone from the keystore, or the file is damaged — and there is no plain
// copy. Nothing is replaced.
var ErrOwnerKeyUnreadable = errors.New("owner key cannot be read")

// ErrOwnerKeyConflict: device_ed25519 holds a valid key that is NOT the one
// in device_ed25519.sealed (a key minted by an older version after a
// downgrade, or restored from a backup). Neither is touched; the person
// decides.
var ErrOwnerKeyConflict = errors.New("two owner keys on disk")

func deviceKeySealedPath() (string, error) {
	dir, err := reminalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, deviceKeySealedName), nil
}

// ownerKeyHint turns the owner-key failures into one line a person can act
// on. Other errors pass through.
func ownerKeyHint(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, atrest.ErrCurrentKeyMissing):
		return fmt.Errorf("%w: the key this machine's saved details are protected with is missing from its store, so the owner key can't be opened; if sessions are running, `reminal doctor --repair-key` puts it back", ErrOwnerKeyLocked)
	case errors.Is(err, ErrOwnerKeyLocked), errors.Is(err, atrest.ErrLocked):
		if runtime.GOOS == "darwin" {
			return fmt.Errorf("%w: your login keychain is locked, so this device's owner key can't be read. Run `security unlock-keychain ~/Library/Keychains/login.keychain-db` and try again", ErrOwnerKeyLocked)
		}
		return fmt.Errorf("%w: the keyring holding this device's owner key isn't available right now (log in to the desktop, or unlock it) and try again", ErrOwnerKeyLocked)
	case errors.Is(err, ErrOwnerKeyUnreadable):
		return fmt.Errorf("%w: %s can't be opened, so this device can't prove it owns anything. Nothing was changed. `reminal own reset` makes a new identity, which you then enrol on each machine again with `sudo reminal add owner`", ErrOwnerKeyUnreadable, deviceKeySealedName)
	case errors.Is(err, ErrOwnerKeyConflict):
		return fmt.Errorf("%w: device_ed25519 and %s hold different keys (one may have been made by an older version, or restored from a backup). Nothing was changed. Move the one you don't want aside, or run `reminal own reset` for a new identity", ErrOwnerKeyConflict, deviceKeySealedName)
	}
	return err
}

// ---- what is on disk --------------------------------------------------------------

type plainKind int

const (
	plainAbsent plainKind = iota
	plainSentinel
	plainValid
	plainCorrupt
)

// diskState is both files, read and nothing more.
type diskState struct {
	sealed    ed25519.PrivateKey
	sealedErr error // nil: opened; os.ErrNotExist; atrest.ErrLocked; errSealedBad
	plain     ed25519.PrivateKey
	plainKind plainKind
	plainPath string
	sealedP   string
}

var errSealedBad = errors.New("sealed owner key cannot be opened")

func readDiskState(quiet bool) (diskState, error) {
	var st diskState
	var err error
	if st.sealedP, err = deviceKeySealedPath(); err != nil {
		return st, err
	}
	if st.plainPath, err = deviceKeyPath(); err != nil {
		return st, err
	}
	blob, serr := os.ReadFile(st.sealedP)
	switch {
	case serr == nil:
		open := atrest.Open
		if quiet {
			open = atrest.OpenQuiet
		}
		pt, oerr := open(deviceKeyKind, deviceKeyID, blob)
		switch {
		case oerr == nil && len(pt) == ed25519.PrivateKeySize:
			st.sealed = ed25519.PrivateKey(pt)
		case errors.Is(oerr, atrest.ErrLocked):
			st.sealedErr = atrest.ErrLocked
		default:
			st.sealedErr = errSealedBad
		}
	case errors.Is(serr, os.ErrNotExist):
		st.sealedErr = os.ErrNotExist
	default:
		return st, serr
	}
	b, perr := os.ReadFile(st.plainPath)
	switch {
	case errors.Is(perr, os.ErrNotExist):
		st.plainKind = plainAbsent
	case perr != nil:
		return st, perr
	case isDeviceKeySentinel(b):
		st.plainKind = plainSentinel
	default:
		raw, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
		if derr == nil && len(raw) == ed25519.PrivateKeySize {
			st.plain, st.plainKind = ed25519.PrivateKey(raw), plainValid
		} else {
			st.plainKind = plainCorrupt
		}
	}
	return st, nil
}

// action is what the state asks for on disk, if anything.
type action int

const (
	actNone     action = iota
	actSentinel        // the plain file holds the same key as the sealed one: replace it with the sentinel
	actSeal            // seal the plain key (no usable sealed copy)
)

// resolve decides which key this device has, and what, if anything, should
// be written. It never asks for a write that would lose a key.
func resolve(st diskState) (ed25519.PrivateKey, action, error) {
	switch {
	case st.sealedErr == nil: // the sealed copy opened
		switch st.plainKind {
		case plainValid:
			if st.plain.Equal(st.sealed) {
				return st.sealed, actSentinel, nil
			}
			return nil, actNone, ErrOwnerKeyConflict
		case plainAbsent:
			return st.sealed, actSentinel, nil
		default: // sentinel, or junk that is not a key
			return st.sealed, actNone, nil
		}
	case errors.Is(st.sealedErr, atrest.ErrLocked):
		if st.plainKind == plainValid {
			// A full identity in the clear beside a sealed copy that cannot
			// be opened yet: usable now, and nothing is written, since
			// whether the two are the same key cannot be told until the
			// keystore answers. (A plain key with NO sealed copy is sealed
			// at once whatever the keystore does: atrest falls back to its
			// key file.)
			return st.plain, actNone, nil
		}
		return nil, actNone, ErrOwnerKeyLocked
	case errors.Is(st.sealedErr, os.ErrNotExist):
		switch st.plainKind {
		case plainValid:
			return st.plain, actSeal, nil
		case plainSentinel:
			return nil, actNone, ErrOwnerKeyUnreadable // its sealed copy is gone
		case plainCorrupt:
			return nil, actNone, fmt.Errorf("key at %s is corrupt; move it aside to mint a new identity", st.plainPath)
		}
		return nil, actNone, os.ErrNotExist
	default: // the sealed file is damaged, or its key is gone
		if st.plainKind == plainValid {
			return st.plain, actSeal, nil // the plain key is the identity; the sealed copy is remade from it
		}
		return nil, actNone, ErrOwnerKeyUnreadable
	}
}

// withDeviceKeyLock serialises every writer of the two files, across
// processes. A lock that cannot be had counts as locked.
func withDeviceKeyLock(fn func() error) error {
	dir, err := reminalDir()
	if err != nil {
		return err
	}
	if err := atrest.CheckWritable(); err != nil {
		return err // a test binary with the real HOME: not even the directory
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	unlock, err := atrest.Lock(dir, deviceKeyLock, deviceKeyLockWait)
	if err != nil {
		return ErrOwnerKeyLocked
	}
	defer unlock()
	return fn()
}

// apply performs an action under the lock, re-reading first: another process
// may have done it, or changed what is there.
func apply(want action) error {
	return withDeviceKeyLock(func() error {
		st, err := readDiskState(false)
		if err != nil {
			return err
		}
		key, act, err := resolve(st)
		_ = os.Remove(st.sealedP + ".new") // a crash mid-seal; stale once anything else resolved
		if err != nil || act != want {
			return err
		}
		switch act {
		case actSentinel:
			return atomicfile.Write(st.plainPath, []byte(deviceKeySentinel), 0o600)
		case actSeal:
			return sealLocked(key)
		}
		return nil
	})
}

// sealLocked writes the sealed copy, proves it opens, then leaves the
// sentinel where the plain key was. The plain copy is never replaced before
// the sealed one has been read back. Caller holds the lock.
func sealLocked(priv ed25519.PrivateKey) error {
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
	// Written beside, read back, then moved into place: a half-written
	// sealed file never sits where it could shadow the plain key.
	tmp := sp + ".new"
	if err := atomicfile.Write(tmp, blob, 0o600); err != nil {
		return err
	}
	back, err := os.ReadFile(tmp)
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if pt, err := atrest.Open(deviceKeyKind, deviceKeyID, back); err != nil || string(pt) != string(priv) {
		_ = os.Remove(tmp)
		return fmt.Errorf("sealed owner key does not read back")
	}
	// Whatever sat at .sealed could not be opened here, but a well-formed
	// blob whose key has gone would open again if the keychain came back (a
	// Time Machine restore of the login keychain): set it aside, as saved
	// sessions are, rather than write over it.
	if _, err := os.Lstat(sp); err == nil {
		dir := filepath.Dir(sp)
		_ = atrest.Quarantine(dir, "device_ed25519", "owner key: the sealed copy could not be opened and was remade from the plain key", sp)
	}
	if err := os.Rename(tmp, sp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return atomicfile.Write(plain, []byte(deviceKeySentinel), 0o600)
}

func isDeviceKeySentinel(b []byte) bool {
	return strings.HasPrefix(strings.TrimSpace(string(b)), "sealed:")
}

// ---- the key ------------------------------------------------------------------------

// loadDeviceKey reads the key as it is on disk, doing whatever safe write the
// state asks for (sealing an older version's plain key, finishing a
// migration). os.ErrNotExist when there is none at all.
func loadDeviceKey() (ed25519.PrivateKey, error) {
	st, err := readDiskState(false)
	if err != nil {
		return nil, err
	}
	key, act, err := resolve(st)
	if err != nil {
		return nil, err
	}
	if act != actNone {
		_ = apply(act) // best effort: the key is usable either way
	}
	return key, nil
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
	var out ed25519.PrivateKey
	err = withDeviceKeyLock(func() error {
		// Whoever waited on the lock may be looking at a key the winner just
		// wrote; take that one rather than replacing it.
		st, err := readDiskState(false)
		if err != nil {
			return err
		}
		key, act, err := resolve(st)
		if err == nil {
			if act == actSeal {
				_ = sealLocked(key)
			} else if act == actSentinel {
				_ = atomicfile.Write(st.plainPath, []byte(deviceKeySentinel), 0o600)
			}
			out = key
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return mintLocked(&out)
	})
	return out, ownerKeyHint(err)
}

// mintLocked makes a new identity. Caller holds both locks.
func mintLocked(out *ed25519.PrivateKey) error {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := sealLocked(priv); err != nil {
		if cerr := atrest.CheckWritable(); cerr != nil {
			return cerr // a test binary with the real HOME: nothing is written
		}
		// Nothing to seal with (atrest already fell back to its key file;
		// this is rarer still). The identity must exist from its first
		// use, so it goes down the old way and is sealed at the next use.
		plain, perr := deviceKeyPath()
		if perr != nil {
			return perr
		}
		if werr := atomicfile.Write(plain, []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600); werr != nil {
			return werr
		}
	}
	*out = priv
	return nil
}

// HasDeviceKey reports whether this device has an identity on disk, readable
// or not (a sentinel alone means one existed), WITHOUT minting one. Used to
// decide whether to try a PIN-free connect before asking for a PIN; an
// identity that cannot be read then says so once.
func HasDeviceKey() bool {
	for _, f := range []func() (string, error){deviceKeySealedPath, deviceKeyPath} {
		if p, err := f(); err == nil {
			if _, err := os.Stat(p); err == nil {
				return true
			}
		}
	}
	return false
}

// OwnerKeyState describes the owner key for `reminal doctor`: "none",
// "sealed", "plain" (not yet sealed), "locked", "conflict" or "unreadable".
// It only looks; a check writes nothing, not even a keystore probe.
func OwnerKeyState() string {
	st, err := readDiskState(true)
	if err != nil {
		return "unreadable"
	}
	_, act, rerr := resolve(st)
	switch {
	case errors.Is(rerr, os.ErrNotExist):
		return "none"
	case errors.Is(rerr, ErrOwnerKeyLocked):
		return "locked"
	case errors.Is(rerr, ErrOwnerKeyConflict):
		return "conflict"
	case rerr != nil:
		return "unreadable"
	case act == actSeal:
		return "plain"
	case st.sealedErr == nil:
		return "sealed"
	}
	return "plain" // readable in the clear while its keystore is locked
}

// ResetDeviceKey throws this device's owner identity away and makes a new one.
// Every machine that knew the old one must be told the new id (`reminal own`,
// then `sudo reminal add owner` there). The caller confirms with the person.
// The id returned is read back from disk, so it is the one that will be used.
func ResetDeviceKey() (newID string, err error) {
	keyMintMu.Lock()
	defer keyMintMu.Unlock()
	var priv ed25519.PrivateKey
	err = withDeviceKeyLock(func() error {
		for _, f := range []func() (string, error){deviceKeySealedPath, deviceKeyPath} {
			p, err := f()
			if err != nil {
				return err
			}
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		var minted ed25519.PrivateKey
		if err := mintLocked(&minted); err != nil {
			return err
		}
		st, err := readDiskState(false)
		if err != nil {
			return err
		}
		key, _, err := resolve(st)
		if err != nil {
			return err
		}
		if !key.Equal(minted) {
			return errors.New("the new owner key did not read back from disk")
		}
		priv = key
		return nil
	})
	if err != nil {
		return "", ownerKeyHint(err)
	}
	return ownerID(priv.Public().(ed25519.PublicKey)), nil
}

// OwnerKeyProblem is the reason this device cannot act as an owner right now
// (locked, unreadable, two keys), as a line for a person, or nil when it can
// or has no identity yet. For listings that would otherwise just show every
// machine as offline.
func OwnerKeyProblem() error {
	_, err := loadDeviceKey()
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return ownerKeyHint(err)
}
