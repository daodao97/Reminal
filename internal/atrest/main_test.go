// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package atrest

import (
	"os"
	"testing"
)

// TestMain points HOME at a scratch directory for the whole package, so no
// test can reach the person's own ~/.reminal: since 3.15.11 anything that
// writes a record seals it, and a test binary minting the at-rest key in the
// real home is the key the person's daemon then adopts (2026-10-04). The
// atrest guard refuses such writes too; this keeps them from being tried.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "reminal-test-home-*")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", dir)
	os.Setenv("USERPROFILE", dir)
	os.Setenv("REMINAL_KEYSTORE", "file")
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
