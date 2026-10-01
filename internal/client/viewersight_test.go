// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import "testing"

// Viewers say their terminal is out of sight per tab; the record carries how
// many of the attached are away, never more than are attached, and forgets it
// with the sizes when anyone leaves.
func TestViewerSight(t *testing.T) {
	var s viewerSight
	if s.awayOf(2) != 0 {
		t.Fatal("away before anyone said so")
	}
	if !s.note("tab-a", true) || s.awayOf(2) != 1 {
		t.Fatal("a tab going away was not counted")
	}
	if s.note("tab-a", true) {
		t.Error("the same report twice counted as a change")
	}
	s.note("tab-b", true)
	if s.awayOf(1) != 1 {
		t.Errorf("more away (%d) than attached (1)", s.awayOf(1))
	}
	if !s.note("tab-a", false) || s.awayOf(2) != 1 {
		t.Error("a tab coming back was not counted")
	}
	s.clear()
	if s.awayOf(2) != 0 {
		t.Error("clear kept reports")
	}

	a := &Agent{sessionID: "SIGHT001"}
	a.sight.note("tab-a", true)
	if r := a.activeRecord(2); r.Viewers != 2 || r.Away != 1 {
		t.Errorf("record: viewers %d away %d, want 2 and 1", r.Viewers, r.Away)
	}
}
