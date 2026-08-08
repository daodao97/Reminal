// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import "testing"

// TestDirHostLockSingleHolder is the regression guard for the v2.0.0 flapping
// bug: at most one agent may hold the directory-host lock at a time, and a
// standing-by agent can take over the instant the holder releases it.
func TestDirHostLockSingleHolder(t *testing.T) {
	isolateHome(t)

	first, ok := tryLockDirHost()
	if !ok {
		t.Fatal("first lock attempt should succeed on a fresh machine")
	}

	// A sibling must NOT get the lock while the first holds it — otherwise two
	// hosts would race the relay channel and the machine flaps.
	if second, ok := tryLockDirHost(); ok {
		unlockDirHost(second)
		t.Fatal("second lock attempt succeeded while the first was held — no single-host guarantee")
	}

	// Release, and the sibling should now be able to take over.
	unlockDirHost(first)
	third, ok := tryLockDirHost()
	if !ok {
		t.Fatal("lock should be acquirable again after the holder releases it")
	}
	unlockDirHost(third)
}
