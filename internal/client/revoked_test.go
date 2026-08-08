// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"crypto/ed25519"
	"crypto/rand"
	"sync"
	"testing"
)

// TestRevokeSelfGatesIsOwner: a self-revoked device stops being an owner even
// though it's still enrolled in owners.json, and restoring re-enables it.
func TestRevokeSelfGatesIsOwner(t *testing.T) {
	isolateHome(t)
	id, err := MyOwnerID()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := AddOwner(id, "self"); err != nil {
		t.Fatal(err)
	}
	dev, _ := loadOrCreateDeviceKey()
	pub := dev.Public().(ed25519.PublicKey)

	if ok, _ := IsOwner(pub); !ok {
		t.Fatal("freshly enrolled device should be an owner")
	}
	if err := RevokeSelf(pub); err != nil {
		t.Fatal(err)
	}
	if ok, _ := IsOwner(pub); ok {
		t.Fatal("self-revoked device must NOT be an owner")
	}
	// Still enrolled in owners.json (revocation is a separate tombstone).
	of, _ := loadOwners()
	if len(of.Owners) != 1 {
		t.Fatalf("owner should remain enrolled, got %d", len(of.Owners))
	}
	// Restore re-enables it.
	if cleared, err := RestoreOwner(pub); err != nil || !cleared {
		t.Fatalf("restore: cleared=%v err=%v", cleared, err)
	}
	if ok, _ := IsOwner(pub); !ok {
		t.Fatal("restored device should be an owner again")
	}
}

func TestRevokeSelfIdempotent(t *testing.T) {
	isolateHome(t)
	dev, _ := loadOrCreateDeviceKey()
	pub := dev.Public().(ed25519.PublicKey)
	if err := RevokeSelf(pub); err != nil {
		t.Fatal(err)
	}
	if err := RevokeSelf(pub); err != nil { // second time is a no-op
		t.Fatal(err)
	}
	if ok, _ := IsRevoked(pub); !ok {
		t.Fatal("should be revoked")
	}
}

// TestRevokeSelfConcurrent guards the tombstone store against the lost-update
// race — the directory host revokes in goroutines, so concurrent self-revokes
// must all land (a dropped tombstone = a "revoked" device that stays an owner).
func TestRevokeSelfConcurrent(t *testing.T) {
	isolateHome(t)
	const n = 20
	pubs := make([]ed25519.PublicKey, n)
	for i := range pubs {
		pubs[i], _, _ = ed25519.GenerateKey(rand.Reader)
	}
	var wg sync.WaitGroup
	for _, p := range pubs {
		wg.Add(1)
		go func(p ed25519.PublicKey) { defer wg.Done(); _ = RevokeSelf(p) }(p)
	}
	wg.Wait()
	for i, p := range pubs {
		if ok, _ := IsRevoked(p); !ok {
			t.Fatalf("tombstone %d lost to a concurrent write", i)
		}
	}
}

func TestRestoreOwnerTargetWasRevoked(t *testing.T) {
	isolateHome(t)
	id, _ := MyOwnerID()
	if _, _, err := AddOwner(id, "self"); err != nil {
		t.Fatal(err)
	}
	dev, _ := loadOrCreateDeviceKey()
	pub := dev.Public().(ed25519.PublicKey)
	// Not revoked → wasRevoked false.
	if _, n, was, err := RestoreOwnerTarget(id); err != nil || n != 1 || was {
		t.Fatalf("non-revoked restore: n=%d was=%v err=%v", n, was, err)
	}
	// Revoke then restore → wasRevoked true, and active again.
	if err := RevokeSelf(pub); err != nil {
		t.Fatal(err)
	}
	if _, n, was, err := RestoreOwnerTarget(id); err != nil || n != 1 || !was {
		t.Fatalf("revoked restore: n=%d was=%v err=%v", n, was, err)
	}
	if ok, _ := IsOwner(pub); !ok {
		t.Fatal("should be active after restore")
	}
}
