// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package session

import (
	"testing"
	"time"
)

// A Windows hot restart hands a long-lived session to a brand-new process, so
// the serving process's start time is far later than the session's birth.
// The PID-reuse check must anchor on the PROCESS time when recorded, or every
// restarted session older than the tolerance is pruned as a recycled-PID
// phantom (the exact field failure that motivated PidStartedAt).
func TestPidAnchorPrefersProcessStart(t *testing.T) {
	sessionBirth := time.Now().Add(-10 * time.Minute)
	procStart := time.Now().Add(-5 * time.Second)

	withAnchor := Active{StartedAt: sessionBirth, PidStartedAt: procStart}
	if got := withAnchor.pidAnchor(); !got.Equal(procStart) {
		t.Fatalf("anchor = %v, want process start %v", got, procStart)
	}
	// Legacy records (no PidStartedAt) fall back to the session birth, which
	// keeps the old same-process semantics on Unix.
	legacy := Active{StartedAt: sessionBirth}
	if got := legacy.pidAnchor(); !got.Equal(sessionBirth) {
		t.Fatalf("legacy anchor = %v, want session birth %v", got, sessionBirth)
	}
}

// SelfStartTime must be stable across calls — record rewrites stamp it
// repeatedly and a moving anchor would defeat the reuse check.
func TestSelfStartTimeStable(t *testing.T) {
	a, b := SelfStartTime(), SelfStartTime()
	if !a.Equal(b) {
		t.Fatalf("SelfStartTime moved: %v vs %v", a, b)
	}
	if time.Since(a) < 0 || time.Since(a) > time.Hour {
		t.Fatalf("implausible self start time: %v", a)
	}
}
