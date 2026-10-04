// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"reminal/internal/atrest"
)

// oldParse is how versions before sealing read device_ed25519.
func oldParse(b []byte) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(raw) != ed25519.PrivateKeySize {
		return nil, errors.New("corrupt")
	}
	return raw, nil
}

func TestDeviceKeyMintedSealed(t *testing.T) {
	isolateReminalHome(t)
	atrest.ResetCacheForTest()
	k, err := loadOrCreateDeviceKey()
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := reminalDir()
	for _, n := range []string{"device_ed25519", "device_ed25519.sealed", "atrest.key"} {
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		if bytes.Contains(b, []byte(base64.StdEncoding.EncodeToString(k))) || bytes.Contains(b, k) {
			t.Fatalf("%s holds the private key in the clear", n)
		}
	}
	plain, _ := os.ReadFile(filepath.Join(dir, "device_ed25519"))
	if _, err := oldParse(plain); err == nil {
		t.Fatal("an older version would parse the sentinel as a key")
	}
	k2, err := loadOrCreateDeviceKey()
	if err != nil || !k2.Equal(k) {
		t.Fatalf("second load: %v", err)
	}
	if !HasDeviceKey() || OwnerKeyState() != "sealed" {
		t.Fatalf("has=%v state=%s", HasDeviceKey(), OwnerKeyState())
	}
}

// A plain key from an older version is read, sealed, and replaced by the
// sentinel — the same identity throughout.
func TestDeviceKeyMigrates(t *testing.T) {
	isolateReminalHome(t)
	atrest.ResetCacheForTest()
	dir, _ := reminalDir()
	_ = os.MkdirAll(dir, 0o700)
	_, priv, _ := ed25519.GenerateKey(nil)
	_ = os.WriteFile(filepath.Join(dir, "device_ed25519"), []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600)
	if !HasDeviceKey() || OwnerKeyState() != "plain" {
		t.Fatalf("before: has=%v state=%s", HasDeviceKey(), OwnerKeyState())
	}
	k, err := loadOrCreateDeviceKey()
	if err != nil || !k.Equal(priv) {
		t.Fatalf("migrated key differs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "device_ed25519.sealed")); err != nil {
		t.Fatal("no sealed copy")
	}
	plain, _ := os.ReadFile(filepath.Join(dir, "device_ed25519"))
	if !isDeviceKeySentinel(plain) {
		t.Fatal("plain key still on disk")
	}
	if OwnerKeyState() != "sealed" {
		t.Fatal(OwnerKeyState())
	}
}

// With the keystore not answering: the plain key is still usable and stays
// plain; a sealed key is "locked", and nothing is minted or replaced.
func TestDeviceKeyLockedNeverMints(t *testing.T) {
	isolateReminalHome(t)
	atrest.ResetCacheForTest()
	k, err := loadOrCreateDeviceKey()
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := reminalDir()
	before, _ := os.ReadFile(filepath.Join(dir, "device_ed25519.sealed"))
	atrest.UnavailableForTest(true)
	defer atrest.UnavailableForTest(false)
	if _, err := loadOrCreateDeviceKey(); !errors.Is(err, ErrOwnerKeyLocked) {
		t.Fatalf("locked: %v", err)
	}
	if _, err := MyOwnerID(); !errors.Is(err, ErrOwnerKeyLocked) || !strings.Contains(err.Error(), "owner key") {
		t.Fatalf("MyOwnerID while locked: %v", err)
	}
	if OwnerKeyState() != "locked" || !HasDeviceKey() {
		t.Fatalf("state=%s has=%v", OwnerKeyState(), HasDeviceKey())
	}
	after, _ := os.ReadFile(filepath.Join(dir, "device_ed25519.sealed"))
	if !bytes.Equal(before, after) {
		t.Fatal("sealed key changed while locked")
	}
	atrest.UnavailableForTest(false)
	if k2, err := loadOrCreateDeviceKey(); err != nil || !k2.Equal(k) {
		t.Fatalf("after unlock: %v", err)
	}
	// An older plain key while the keystore gives nothing at all (only
	// UnavailableForTest does this; a real locked keystore falls back to the
	// key file and seals at once): still works, still plain.
	isolateReminalHome(t)
	atrest.ResetCacheForTest()
	dir, _ = reminalDir()
	_ = os.MkdirAll(dir, 0o700)
	_, priv, _ := ed25519.GenerateKey(nil)
	_ = os.WriteFile(filepath.Join(dir, "device_ed25519"), []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600)
	atrest.UnavailableForTest(true)
	if k, err := loadOrCreateDeviceKey(); err != nil || !k.Equal(priv) {
		t.Fatalf("plain while locked: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "device_ed25519.sealed")); err == nil {
		t.Fatal("sealed while the keystore was not answering")
	}
}

// A sealed key whose at-rest key is gone, or a sentinel with no sealed copy:
// reported, never replaced; only reset makes a new identity.
func TestDeviceKeyUnreadableIsReportedNotReplaced(t *testing.T) {
	isolateReminalHome(t)
	atrest.ResetCacheForTest()
	k, _ := loadOrCreateDeviceKey()
	dir, _ := reminalDir()
	// The sealing key FILE missing is damage: locked, nothing replaced.
	_ = os.Remove(filepath.Join(dir, "atrest.key"))
	atrest.ResetCacheForTest()
	if _, err := loadOrCreateDeviceKey(); !errors.Is(err, ErrOwnerKeyLocked) {
		t.Fatalf("sealing key file missing: %v, want ErrOwnerKeyLocked", err)
	}
	// A real rotation: unreadable, reported, never replaced.
	atrest.RotateForTest()
	if _, err := loadOrCreateDeviceKey(); !errors.Is(err, ErrOwnerKeyUnreadable) || !strings.Contains(err.Error(), "own reset") {
		t.Fatalf("key gone: %v", err)
	}
	if OwnerKeyState() != "unreadable" {
		t.Fatal(OwnerKeyState())
	}
	sealed, _ := os.ReadFile(filepath.Join(dir, "device_ed25519.sealed"))
	if len(sealed) == 0 {
		t.Fatal("sealed copy removed")
	}
	_ = os.Remove(filepath.Join(dir, "device_ed25519.sealed"))
	if _, err := loadOrCreateDeviceKey(); !errors.Is(err, ErrOwnerKeyUnreadable) {
		t.Fatalf("sentinel without sealed copy: %v", err)
	}
	if !HasDeviceKey() {
		t.Fatal("a sentinel alone means an identity existed; HasDeviceKey must say so, so connect can explain")
	}
	id, err := ResetDeviceKey()
	if err != nil || !strings.HasPrefix(id, OwnerIDPrefix) {
		t.Fatalf("reset: %q %v", id, err)
	}
	k2, err := loadOrCreateDeviceKey()
	if err != nil || k2.Equal(k) {
		t.Fatalf("after reset: same key or %v", err)
	}
	if OwnerKeyState() != "sealed" {
		t.Fatal(OwnerKeyState())
	}
}

// Reviewer's cases: the code must never destroy or hide a VALID plain key.

// A sealed key B and a different valid plain key A (minted by an older
// version after a downgrade, or restored from a backup): neither is touched,
// the conflict is reported, and reset is the only way out.
func TestAdvPlainKeyDifferentFromSealedNotDestroyed(t *testing.T) {
	isolateReminalHome(t)
	atrest.ResetCacheForTest()
	if _, err := loadOrCreateDeviceKey(); err != nil {
		t.Fatal(err)
	} // sealed = key B
	dir, _ := reminalDir()
	sealedBefore, _ := os.ReadFile(filepath.Join(dir, "device_ed25519.sealed"))
	_, kA, _ := ed25519.GenerateKey(nil)
	_ = os.WriteFile(filepath.Join(dir, "device_ed25519"), []byte(base64.StdEncoding.EncodeToString(kA)+"\n"), 0o600)
	_, err := loadOrCreateDeviceKey()
	if !errors.Is(err, ErrOwnerKeyConflict) {
		t.Fatalf("want ErrOwnerKeyConflict, got %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "device_ed25519"))
	if raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b))); err != nil || !bytes.Equal(raw, kA) {
		t.Fatalf("plain key A overwritten: %q", strings.TrimSpace(string(b)))
	}
	if after, _ := os.ReadFile(filepath.Join(dir, "device_ed25519.sealed")); !bytes.Equal(after, sealedBefore) {
		t.Fatal("sealed key B changed")
	}
	if OwnerKeyState() != "conflict" {
		t.Fatal(OwnerKeyState())
	}
	if _, err := ResetDeviceKey(); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreateDeviceKey(); err != nil {
		t.Fatalf("after reset: %v", err)
	}
}

// Junk where the sealed file should be, beside a valid plain key: the plain
// key is the identity, and the sealed copy is remade from it.
func TestAdvJunkSealedBesideValidPlain(t *testing.T) {
	isolateReminalHome(t)
	atrest.ResetCacheForTest()
	dir, _ := reminalDir()
	_ = os.MkdirAll(dir, 0o700)
	_, kA, _ := ed25519.GenerateKey(nil)
	for _, junk := range []string{"junk", ""} {
		_ = os.WriteFile(filepath.Join(dir, "device_ed25519"), []byte(base64.StdEncoding.EncodeToString(kA)+"\n"), 0o600)
		_ = os.WriteFile(filepath.Join(dir, "device_ed25519.sealed"), []byte(junk), 0o600)
		got, err := loadOrCreateDeviceKey()
		if err != nil || !got.Equal(kA) {
			t.Fatalf("%q: valid plain key shadowed: %v", junk, err)
		}
		atrest.ResetCacheForTest()
		if got, err := loadOrCreateDeviceKey(); err != nil || !got.Equal(kA) {
			t.Fatalf("%q: after reseal: %v", junk, err)
		}
		if OwnerKeyState() != "sealed" {
			t.Fatalf("%q: state %s", junk, OwnerKeyState())
		}
	}
	// The unopenable sealed files were set aside, not written over.
	q, _ := os.ReadDir(filepath.Join(dir, "quarantine"))
	var names []string
	for _, e := range q {
		names = append(names, e.Name())
	}
	if n := strings.Count(strings.Join(names, " "), "device_ed25519.sealed"); n != 2 {
		t.Fatalf("quarantine holds %v, want two set-aside sealed copies", names)
	}
}

// Reset racing a migration of the old plain key: the id reset prints is the
// one on disk afterwards, every time.
func TestAdvResetRacesMigration(t *testing.T) {
	mismatch := 0
	for i := 0; i < 150; i++ {
		isolateReminalHome(t)
		atrest.ResetCacheForTest()
		dir, _ := reminalDir()
		_ = os.MkdirAll(dir, 0o700)
		_, priv, _ := ed25519.GenerateKey(nil)
		_ = os.WriteFile(filepath.Join(dir, "device_ed25519"), []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600)
		var wg sync.WaitGroup
		var resetID string
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = loadOrCreateDeviceKey() }()
		go func() { defer wg.Done(); resetID, _ = ResetDeviceKey() }()
		wg.Wait()
		atrest.ResetCacheForTest()
		k, err := loadOrCreateDeviceKey()
		if err != nil || ownerID(k.Public().(ed25519.PublicKey)) != resetID {
			mismatch++
		}
	}
	if mismatch > 0 {
		t.Fatalf("%d/150: reset printed an id that is not on disk", mismatch)
	}
}
