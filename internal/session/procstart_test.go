// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package session

import (
	"os"
	"testing"
	"time"
)

// TestPidReuseDetection guards the phantom-row fix: a live PID whose process is
// far newer than the recorded session must be treated as reused (pruned), while
// a genuine record and a legacy zero-time record must not be.
func TestPidReuseDetection(t *testing.T) {
	self := os.Getpid()
	if _, ok := procStartTime(self); !ok {
		t.Skip("procStartTime unsupported on this platform")
	}

	// This process started before now, so a record written "just now" for our own
	// PID is genuine — not reuse.
	if pidReused(self, time.Now()) {
		t.Fatal("current process wrongly flagged as a reused PID (would prune a live session)")
	}

	// A record supposedly written days ago, but our (far newer) process now holds
	// the PID → the OS recycled it → reuse detected.
	if !pidReused(self, time.Now().Add(-72*time.Hour)) {
		t.Fatal("a stale record on a now-newer PID should be flagged as reused")
	}

	// Legacy records without a StartedAt must never be pruned as reused.
	if pidReused(self, time.Time{}) {
		t.Fatal("zero StartedAt must not be treated as reuse")
	}
}
