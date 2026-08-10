// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package config

import "testing"

// TestSaveLoadRoundTrip covers SaveSettings -> LoadSettings, including the
// rename-over-existing path of the atomic write.
func TestSaveLoadRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	in := Settings{StayUnlocked: true, ClosedLid: true}
	if err := SaveSettings(in); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := LoadSettings(); got.StayUnlocked != true || got.ClosedLid != true {
		t.Fatalf("round-trip mismatch: got %+v want %+v", got, in)
	}

	// Overwriting an existing file exercises the atomic rename-over-existing path.
	if err := SaveSettings(Settings{StayUnlocked: false, ClosedLid: false}); err != nil {
		t.Fatalf("overwrite save: %v", err)
	}
	if got := LoadSettings(); got.StayUnlocked || got.ClosedLid {
		t.Fatalf("overwrite not applied: %+v", got)
	}
}

// TestLoadMissingReturnsZero: no file -> zero-value settings, no error surface.
func TestLoadMissingReturnsZero(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := LoadSettings(); got.StayUnlocked || got.ClosedLid {
		t.Fatalf("expected zero-value settings with no file, got %+v", got)
	}
}
