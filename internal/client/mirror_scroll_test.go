// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"runtime"
	"testing"
	"time"
)

// A window must be raised when it is not already the front one, and NOT
// re-raised while it stays there. Keying this on gesture timing could not
// work: the raise costs ~410ms (focus walks every window of the app over the
// Accessibility bridge), so scroll events queued further apart than the 400ms
// "same gesture" window they were tested against, and every one paid the raise
// again. The cost being avoided defeated the test for avoiding it.
func TestMirrorRaisesOnlyWhenTheFrontWindowChanges(t *testing.T) {
	reset := func() {
		mirrorFront = frontWindowTracker{}
	}

	t.Run("first event on a window raises it", func(t *testing.T) {
		reset()
		if !mirrorFront.needsRaise("w1") {
			t.Fatal("a window not known to be in front must be raised")
		}
	})

	t.Run("staying on it does not, however slowly events arrive", func(t *testing.T) {
		reset()
		mirrorFront.needsRaise("w1")
		for i := 0; i < 20; i++ {
			// Pretend each event took longer than the old gesture gap. That is
			// the case the previous rule got wrong.
			mirrorFront.mu.Lock()
			mirrorFront.at = time.Now().Add(-winScrollGestureGap - 200*time.Millisecond)
			mirrorFront.mu.Unlock()
			if mirrorFront.needsRaise("w1") {
				t.Fatalf("event %d re-raised a window already in front", i+2)
			}
		}
	})

	t.Run("a different window raises immediately", func(t *testing.T) {
		reset()
		mirrorFront.needsRaise("w1")
		if !mirrorFront.needsRaise("w2") {
			t.Fatal("switching windows must raise the new one")
		}
		if mirrorFront.needsRaise("w2") {
			t.Fatal("re-raised the window it just raised")
		}
	})

	t.Run("a click elsewhere makes the next scroll re-raise", func(t *testing.T) {
		reset()
		mirrorFront.needsRaise("w1")
		mirrorFront.note("w2") // a click raised something else
		if !mirrorFront.needsRaise("w1") {
			t.Fatal("scrolling w1 after w2 was raised must raise w1 again")
		}
	})

	t.Run("the record goes stale so an external focus change is picked up", func(t *testing.T) {
		reset()
		mirrorFront.needsRaise("w1")
		mirrorFront.mu.Lock()
		mirrorFront.at = time.Now().Add(-frontWindowTTL - time.Millisecond)
		mirrorFront.mu.Unlock()
		if !mirrorFront.needsRaise("w1") {
			t.Fatal("a stale record must be re-established")
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
