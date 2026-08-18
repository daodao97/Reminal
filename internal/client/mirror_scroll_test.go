// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"runtime"
	"testing"
	"time"
)

// The daemon must raise a scrolled window ONCE per gesture, not once per event.
// macOS routes every input through the daemon, so this is the only copy of the
// rule that actually runs there — the in-process one in handleWindowInput is
// unreachable on a Mac. Paying the ~100ms raise per event put the host at half
// the rate the viewer emits scrolls, so the backlog grew for as long as a
// finger moved and kept draining after it stopped.
func TestMirrorScrollRaisesOncePerGesture(t *testing.T) {
	reset := func() {
		mirrorScrollMu.Lock()
		mirrorScrollID, mirrorScrollAt = "", time.Time{}
		mirrorScrollMu.Unlock()
	}

	t.Run("first event of a gesture raises", func(t *testing.T) {
		reset()
		if !mirrorScrollStartsGesture("w1") {
			t.Fatal("the opening event of a gesture must raise the window")
		}
	})

	t.Run("continuing the gesture does not", func(t *testing.T) {
		reset()
		mirrorScrollStartsGesture("w1")
		for i := 0; i < 40; i++ { // ~2s of scrolling at the viewer's 50ms cadence
			if mirrorScrollStartsGesture("w1") {
				t.Fatalf("event %d re-raised mid-gesture", i+2)
			}
		}
	})

	t.Run("switching windows raises the new one", func(t *testing.T) {
		reset()
		mirrorScrollStartsGesture("w1")
		if !mirrorScrollStartsGesture("w2") {
			t.Fatal("scrolling a different window must raise it")
		}
		if mirrorScrollStartsGesture("w2") {
			t.Fatal("second event on the new window re-raised")
		}
	})

	t.Run("a pause starts a new gesture", func(t *testing.T) {
		reset()
		mirrorScrollStartsGesture("w1")
		// Backdate past the gap rather than sleeping for it.
		mirrorScrollMu.Lock()
		mirrorScrollAt = time.Now().Add(-winScrollGestureGap - time.Millisecond)
		mirrorScrollMu.Unlock()
		if !mirrorScrollStartsGesture("w1") {
			t.Fatal("a gesture resumed after the gap must raise again")
		}
	})

	t.Run("just inside the gap is still the same gesture", func(t *testing.T) {
		reset()
		mirrorScrollStartsGesture("w1")
		mirrorScrollMu.Lock()
		mirrorScrollAt = time.Now().Add(-winScrollGestureGap + 50*time.Millisecond)
		mirrorScrollMu.Unlock()
		if mirrorScrollStartsGesture("w1") {
			t.Fatal("an event inside the gap re-raised")
		}
	})
}

// countingBackend records how many times the window list was enumerated. On
// macOS that enumeration is an osascript over every window on the system,
// measured at ~114ms against the live daemon — the single most expensive thing
// an input event did, and it did it every time.
type countingBackend struct {
	windowBackend
	lists int
	wins  []winInfo
}

func (c *countingBackend) list() ([]winInfo, error) { c.lists++; return c.wins, nil }

func TestMirrorFindWindowReusesLookupWithinAGesture(t *testing.T) {
	reset := func() {
		mirrorWinMu.Lock()
		mirrorWinCache = map[string]mirrorWinEntry{}
		mirrorWinMu.Unlock()
	}
	b := func() *countingBackend {
		return &countingBackend{wins: []winInfo{{ID: "w1", W: 800, H: 600}}}
	}

	t.Run("a scroll gesture enumerates once, not per event", func(t *testing.T) {
		reset()
		c := b()
		for i := 0; i < 20; i++ { // a second of scrolling at the viewer's cadence
			if _, err := mirrorFindWindow(c, "w1", true); err != nil {
				t.Fatalf("event %d: %v", i, err)
			}
		}
		if c.lists != 1 {
			t.Fatalf("enumerated %d times for one gesture, want 1", c.lists)
		}
	})

	t.Run("clicks always re-resolve", func(t *testing.T) {
		reset()
		c := b()
		for i := 0; i < 3; i++ {
			// reuse=false is what a click/drag passes: placing a pointer on a
			// stale rectangle is a click in the wrong place.
			if _, err := mirrorFindWindow(c, "w1", false); err != nil {
				t.Fatal(err)
			}
		}
		if c.lists != 3 {
			t.Fatalf("enumerated %d times, want a fresh lookup per click (3)", c.lists)
		}
	})

	t.Run("the entry expires so a moved window is picked up", func(t *testing.T) {
		reset()
		c := b()
		mirrorFindWindow(c, "w1", true)
		mirrorWinMu.Lock()
		e := mirrorWinCache["w1"]
		e.at = time.Now().Add(-winScrollGestureGap - time.Millisecond)
		mirrorWinCache["w1"] = e
		mirrorWinMu.Unlock()
		mirrorFindWindow(c, "w1", true)
		if c.lists != 2 {
			t.Fatalf("enumerated %d times, want a re-resolve after expiry (2)", c.lists)
		}
	})

	t.Run("a closed window is not served from cache", func(t *testing.T) {
		reset()
		c := b()
		mirrorFindWindow(c, "w1", true)
		c.wins = nil // window closed
		mirrorWinMu.Lock()
		e := mirrorWinCache["w1"]
		e.at = time.Now().Add(-winScrollGestureGap - time.Millisecond)
		mirrorWinCache["w1"] = e
		mirrorWinMu.Unlock()
		if _, err := mirrorFindWindow(c, "w1", true); err == nil {
			t.Fatal("resolved a window that is gone")
		}
		mirrorWinMu.Lock()
		_, still := mirrorWinCache["w1"]
		mirrorWinMu.Unlock()
		if still {
			t.Fatal("stale entry survived a failed lookup")
		}
	})
}

// Which events may reuse a resolved window is a correctness question, not just
// a performance one: anything that places a pointer at an exact spot must see
// the window's current rectangle, or the pointer lands somewhere else.
func TestMirrorReuseLookupOnlyWhereGeometryIsStable(t *testing.T) {
	for _, tc := range []struct {
		kind, phase string
		want        bool
		why         string
	}{
		{"scroll", "", true, "repeats many times a second under a fixed pointer"},
		{"key", "", true, "does not use geometry at all"},
		{"drag", "begin", false, "the press must land on the current rectangle"},
		{"drag", "move", true, "the window cannot move out from under a live drag"},
		{"drag", "end", true, "continues the same gesture"},
		{"drag", "", false, "legacy whole-path replay places its own press"},
		{"click", "", false, "a stale rectangle is a click in the wrong place"},
	} {
		if got := mirrorReuseLookup(tc.kind, tc.phase); got != tc.want {
			t.Errorf("mirrorReuseLookup(%q, %q) = %v, want %v — %s",
				tc.kind, tc.phase, got, tc.want, tc.why)
		}
	}
}

// The phased protocol is opt-in per host. A viewer that sent phases to a host
// which replays whole paths would press and release once per chunk — a burst
// of clicks where the user meant one drag — so the flag has to track which
// backends actually implement the phases.
func TestDragPhasesAdvertisedOnlyWhereImplemented(t *testing.T) {
	h := gatherHostInfo()
	if want := runtime.GOOS == "darwin"; h.DragPhases != want {
		t.Fatalf("DragPhases = %v on %s, want %v", h.DragPhases, runtime.GOOS, want)
	}
}
