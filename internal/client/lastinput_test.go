// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"testing"
	"time"
)

// A person typing is recorded apart from output: the record says when
// someone last typed, and nothing but typing moves it.
func TestLastInputIsTypingNotOutput(t *testing.T) {
	a := &Agent{sessionID: "LASTIN01", startedAt: time.Now().Add(-time.Hour)}
	if got := a.activeRecord(0).LastInput; !got.IsZero() {
		t.Fatalf("before any typing: %v", got)
	}
	a.markActivity(time.Now())
	if got := a.activeRecord(0).LastInput; !got.IsZero() {
		t.Fatalf("output moved it: %v", got)
	}
	before := time.Now()
	a.markInput()
	got := a.activeRecord(0).LastInput
	if got.Before(before) || time.Since(got) > time.Second {
		t.Fatalf("after typing: %v", got)
	}
	if !a.metaDirty.Load() {
		t.Error("typing did not mark the record for a rewrite")
	}
}
