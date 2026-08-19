// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package updater

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDisplacedPathIsUnique is the regression test for an upgrade that worked
// exactly once per machine. Displaced binaries are usually still executing (the
// daemon, live agents, pty holders), and Windows will not overwrite a file with
// a mapped image section — so a second upgrade that reached for the same
// ".old" name failed with "Access is denied".
func TestDisplacedPathIsUnique(t *testing.T) {
	dir := t.TempDir()

	first, err := displacedPath(dir)
	if err != nil {
		t.Fatalf("displacedPath: %v", err)
	}
	// Simulate the displaced binary still being there — and, on Windows,
	// unremovable because something is running it.
	if err := os.WriteFile(first, []byte("old binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := displacedPath(dir)
	if err != nil {
		t.Fatalf("displacedPath after one displacement: %v", err)
	}
	if second == first {
		t.Fatalf("second upgrade reused the name of a binary that may still be running: %s", second)
	}
	if !strings.HasPrefix(filepath.Base(second), displacedPrefix) {
		t.Errorf("displaced name %q doesn't carry the prefix the sweep looks for", second)
	}
}

func TestSweepDisplacedClearsLeftovers(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "reminal.exe")
	legacy := filepath.Join(dir, "reminal.exe.old")
	for _, p := range []string{keep, legacy} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var displaced []string
	for i := 0; i < 3; i++ {
		p, err := displacedPath(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		displaced = append(displaced, p)
	}

	sweepDisplaced(dir)

	for _, p := range append(displaced, legacy) {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s survived the sweep", filepath.Base(p))
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("the sweep took the installed binary: %v", err)
	}
}
