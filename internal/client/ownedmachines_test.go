// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

func newMachineKey(t *testing.T) ed25519.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func TestRecordAndListOwnedMachines(t *testing.T) {
	isolateHome(t)
	a := newMachineKey(t)
	b := newMachineKey(t)

	if err := RecordOwnedMachine(a); err != nil {
		t.Fatalf("record a: %v", err)
	}
	if err := RecordOwnedMachine(b); err != nil {
		t.Fatalf("record b: %v", err)
	}
	// Re-recording the same machine must not duplicate it.
	if err := RecordOwnedMachine(a); err != nil {
		t.Fatalf("re-record a: %v", err)
	}

	list, err := ListOwnedMachines()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 owned machines, got %d", len(list))
	}
	// b was recorded after a's first insert but a was touched last → a is most
	// recently seen and should sort first.
	if !list[0].Key.Equal(a) {
		t.Errorf("expected most-recently-seen (a) first, got %x", list[0].Key)
	}
	if list[0].FirstOwned.After(list[0].LastSeen) {
		t.Error("FirstOwned should not be after LastSeen")
	}
}

func TestRecordOwnedMachineRejectsBadKey(t *testing.T) {
	isolateHome(t)
	if err := RecordOwnedMachine([]byte{1, 2, 3}); err == nil {
		t.Fatal("expected error for a short machine key")
	}
}

func TestRenameOwnedMachineByNameAndID(t *testing.T) {
	isolateHome(t)
	k := newMachineKey(t)
	if err := RecordOwnedMachine(k); err != nil {
		t.Fatal(err)
	}

	// Rename by id prefix (the short id, sans the trailing ellipsis).
	if _, err := RenameOwnedMachine(ShortMachineID(k), "laptop"); err != nil {
		t.Fatalf("rename by id: %v", err)
	}
	list, _ := ListOwnedMachines()
	if list[0].Name != "laptop" {
		t.Fatalf("want name laptop, got %q", list[0].Name)
	}

	// Now rename by the name we just set.
	if _, err := RenameOwnedMachine("laptop", "work-laptop"); err != nil {
		t.Fatalf("rename by name: %v", err)
	}
	list, _ = ListOwnedMachines()
	if list[0].Name != "work-laptop" {
		t.Fatalf("want name work-laptop, got %q", list[0].Name)
	}

	// An empty name is rejected.
	if _, err := RenameOwnedMachine("work-laptop", "   "); err == nil {
		t.Error("expected error renaming to an empty name")
	}
	// An unknown selector is rejected.
	if _, err := RenameOwnedMachine("nope", "x"); err == nil {
		t.Error("expected error for unknown machine")
	}
}

// A selector that strips to an empty needle (a bare prefix) must match nothing,
// not silently resolve to an arbitrary machine.
func TestFindOwnedMachineRejectsEmptyNeedle(t *testing.T) {
	isolateHome(t)
	if err := RecordOwnedMachine(newMachineKey(t)); err != nil {
		t.Fatal(err)
	}
	for _, sel := range []string{MachineIDPrefix, "…", MachineIDPrefix + "…"} {
		if _, err := RenameOwnedMachine(sel, "x"); err == nil {
			t.Fatalf("selector %q should not match the sole machine", sel)
		}
	}
	// A real short id still resolves.
	list, _ := ListOwnedMachines()
	if _, err := RenameOwnedMachine(ShortMachineID(list[0].Key), "ok"); err != nil {
		t.Fatalf("real short id should still resolve: %v", err)
	}
}

func TestMachineIDRoundTrip(t *testing.T) {
	k := newMachineKey(t)
	id := MachineID(k)
	if id[:len(MachineIDPrefix)] != MachineIDPrefix {
		t.Fatalf("id missing prefix: %s", id)
	}
	// The short id is a strict prefix of the full id (plus an ellipsis).
	short := ShortMachineID(k)
	if short[len(short)-len("…"):] != "…" {
		t.Errorf("short id should end with an ellipsis: %s", short)
	}
}
