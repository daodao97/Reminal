// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
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
		mirrorInput.front = frontWindowTracker{}
	}

	t.Run("first event on a window raises it", func(t *testing.T) {
		reset()
		if !mirrorInput.front.needsRaise("w1") {
			t.Fatal("a window not known to be in front must be raised")
		}
	})

	t.Run("staying on it does not, however slowly events arrive", func(t *testing.T) {
		reset()
		mirrorInput.front.needsRaise("w1")
		for i := 0; i < 20; i++ {
			// Pretend each event took longer than the old gesture gap. That is
			// the case the previous rule got wrong.
			mirrorInput.front.mu.Lock()
			mirrorInput.front.at = time.Now().Add(-winLookupReuseTTL - 200*time.Millisecond)
			mirrorInput.front.mu.Unlock()
			if mirrorInput.front.needsRaise("w1") {
				t.Fatalf("event %d re-raised a window already in front", i+2)
			}
		}
	})

	t.Run("a different window raises immediately", func(t *testing.T) {
		reset()
		mirrorInput.front.needsRaise("w1")
		if !mirrorInput.front.needsRaise("w2") {
			t.Fatal("switching windows must raise the new one")
		}
		if mirrorInput.front.needsRaise("w2") {
			t.Fatal("re-raised the window it just raised")
		}
	})

	t.Run("a click elsewhere makes the next scroll re-raise", func(t *testing.T) {
		reset()
		mirrorInput.front.needsRaise("w1")
		mirrorInput.front.note("w2") // a click raised something else
		if !mirrorInput.front.needsRaise("w1") {
			t.Fatal("scrolling w1 after w2 was raised must raise w1 again")
		}
	})

	t.Run("the record goes stale so an external focus change is picked up", func(t *testing.T) {
		reset()
		mirrorInput.front.needsRaise("w1")
		mirrorInput.front.mu.Lock()
		mirrorInput.front.at = time.Now().Add(-frontWindowTTL - time.Millisecond)
		mirrorInput.front.mu.Unlock()
		if !mirrorInput.front.needsRaise("w1") {
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
		winLookupMu.Lock()
		winLookupCache = map[string]winLookupEntry{}
		winLookupMu.Unlock()
	}
	b := func() *countingBackend {
		return &countingBackend{wins: []winInfo{{ID: "w1", W: 800, H: 600}}}
	}

	t.Run("a scroll gesture enumerates once, not per event", func(t *testing.T) {
		reset()
		c := b()
		for i := 0; i < 20; i++ { // a second of scrolling at the viewer's cadence
			if _, err := resolveWindowFor(c, "w1", true); err != nil {
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
			if _, err := resolveWindowFor(c, "w1", false); err != nil {
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
		resolveWindowFor(c, "w1", true)
		winLookupMu.Lock()
		e := winLookupCache["w1"]
		e.at = time.Now().Add(-winLookupReuseTTL - time.Millisecond)
		winLookupCache["w1"] = e
		winLookupMu.Unlock()
		resolveWindowFor(c, "w1", true)
		if c.lists != 2 {
			t.Fatalf("enumerated %d times, want a re-resolve after expiry (2)", c.lists)
		}
	})

	t.Run("a closed window is not served from cache", func(t *testing.T) {
		reset()
		c := b()
		resolveWindowFor(c, "w1", true)
		c.wins = nil // window closed
		winLookupMu.Lock()
		e := winLookupCache["w1"]
		e.at = time.Now().Add(-winLookupReuseTTL - time.Millisecond)
		winLookupCache["w1"] = e
		winLookupMu.Unlock()
		if _, err := resolveWindowFor(c, "w1", true); err == nil {
			t.Fatal("resolved a window that is gone")
		}
		winLookupMu.Lock()
		_, still := winLookupCache["w1"]
		winLookupMu.Unlock()
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
		if got := reuseLookupFor(tc.kind, tc.phase); got != tc.want {
			t.Errorf("reuseLookupFor(%q, %q) = %v, want %v — %s",
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

// recordingBackend records what a backend was actually asked to do.
type recordingBackend struct {
	windowBackend
	wins  []winInfo
	calls []string
}

func (r *recordingBackend) list() ([]winInfo, error) { return r.wins, nil }
func (r *recordingBackend) focus(w winInfo) error {
	r.calls = append(r.calls, "focus")
	return nil
}
func (r *recordingBackend) clickN(w winInfo, fx, fy float64, n int, right bool) error {
	r.calls = append(r.calls, fmt.Sprintf("click(n=%d,right=%v)", n, right))
	return nil
}
func (r *recordingBackend) scroll(w winInfo, fx, fy, dx, dy float64) error {
	r.calls = append(r.calls, "scroll")
	return nil
}
func (r *recordingBackend) drag(w winInfo, pts [][2]float64) error {
	r.calls = append(r.calls, fmt.Sprintf("drag(path=%d)", len(pts)))
	return nil
}

func newRecording() *recordingBackend {
	return &recordingBackend{wins: []winInfo{{ID: "w1", W: 800, H: 600}}}
}

// One dispatcher serves both injection paths. It previously existed twice and
// the copies drifted every time something was added: the daemon — the path
// macOS actually takes — never gained the click-count fallback, so an older
// viewer that reports no count got a single click where it meant a double.
func TestApplyWindowInputIsOneImplementation(t *testing.T) {
	fresh := func() (*recordingBackend, *inputState) {
		winLookupMu.Lock()
		winLookupCache = map[string]winLookupEntry{}
		winLookupMu.Unlock()
		return newRecording(), &inputState{}
	}

	t.Run("a viewer that reports no count still gets a double-click", func(t *testing.T) {
		b, st := fresh()
		ev := windowInput{ID: "w1", Kind: "click", X: 0.5, Y: 0.5} // Count unset
		applyWindowInput(b, st, ev, nil)
		applyWindowInput(b, st, ev, nil)
		if got := strings.Join(b.calls, " "); !strings.Contains(got, "click(n=2") {
			t.Fatalf("calls = %q, want a second click counted as n=2", got)
		}
	})

	t.Run("a reported count is trusted over the fallback", func(t *testing.T) {
		b, st := fresh()
		applyWindowInput(b, st, windowInput{ID: "w1", Kind: "click", Count: 3}, nil)
		if got := strings.Join(b.calls, " "); !strings.Contains(got, "click(n=3") {
			t.Fatalf("calls = %q, want the viewer's own count", got)
		}
	})

	t.Run("a backend without phased drag drops phased events", func(t *testing.T) {
		b, st := fresh()
		// recordingBackend implements no dragPhase, standing in for the
		// platforms that never advertised drag_phases.
		applyWindowInput(b, st, windowInput{ID: "w1", Kind: "drag", Phase: "move",
			Path: [][2]float64{{0.1, 0.1}}}, nil)
		if len(b.calls) != 0 {
			t.Fatalf("calls = %v, want a phased event dropped, not replayed as a click burst", b.calls)
		}
	})

	t.Run("a whole-path drag still replays", func(t *testing.T) {
		b, st := fresh()
		applyWindowInput(b, st, windowInput{ID: "w1", Kind: "drag",
			Path: [][2]float64{{0.1, 0.1}, {0.2, 0.2}}}, nil)
		if got := strings.Join(b.calls, " "); !strings.Contains(got, "drag(path=2)") {
			t.Fatalf("calls = %q, want the batched drag", got)
		}
	})

	t.Run("scroll raises once and the click path records the raise", func(t *testing.T) {
		b, st := fresh()
		sc := windowInput{ID: "w1", Kind: "scroll"}
		applyWindowInput(b, st, sc, nil)
		applyWindowInput(b, st, sc, nil)
		if n := strings.Count(strings.Join(b.calls, " "), "focus"); n != 1 {
			t.Fatalf("focus called %d times for one window, want 1", n)
		}
	})

	t.Run("a right-click is reported to the menu hook", func(t *testing.T) {
		b, st := fresh()
		var gotRight, called = false, false
		applyWindowInput(b, st, windowInput{ID: "w1", Kind: "click", Button: "right", Count: 1},
			func(w winInfo, right bool) { called, gotRight = true, right })
		if !called || !gotRight {
			t.Fatalf("menu hook called=%v right=%v, want true/true", called, gotRight)
		}
	})
}

// draggingBackend records phased drag injection.
type draggingBackend struct {
	recordingBackend
	mu     sync.Mutex
	phases []string
}

func (d *draggingBackend) dragPhase(w winInfo, phase string, fx, fy float64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.phases = append(d.phases, phase)
	return nil
}
func (d *draggingBackend) got() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.phases...)
}

// A live drag presses on "begin" and releases on "end", so an end that never
// arrives leaves the host's desktop grabbed. The existing releases only fire
// when the last viewer leaves or a pane closes — neither happens when a socket
// drops mid-drag and the viewer RECONNECTS, so the button stayed down for as
// long as the session lived. The batched drag this replaced could not do that:
// it was one replay that always ended with a mouse-up.
func TestAbandonedDragReleasesTheButton(t *testing.T) {
	newDragging := func() *draggingBackend {
		winLookupMu.Lock()
		winLookupCache = map[string]winLookupEntry{}
		winLookupMu.Unlock()
		d := &draggingBackend{}
		d.wins = []winInfo{{ID: "w1", W: 800, H: 600}}
		return d
	}

	t.Run("a drag that stops arriving is released for it", func(t *testing.T) {
		b, st := newDragging(), &inputState{}
		applyWindowInput(b, st, windowInput{ID: "w1", Kind: "drag", Phase: "begin", X: 0.2, Y: 0.2}, nil)
		applyWindowInput(b, st, windowInput{ID: "w1", Kind: "drag", Phase: "move",
			Path: [][2]float64{{0.5, 0.5}}}, nil)
		// The viewer vanishes here — no "end" ever comes.
		st.drag.mu.Lock()
		armed := st.drag.timer != nil
		st.drag.mu.Unlock()
		if !armed {
			t.Fatal("no watchdog armed for a live drag")
		}
		// Fire it now rather than waiting out dragStallTimeout.
		st.drag.mu.Lock()
		st.drag.timer.Reset(time.Millisecond)
		st.drag.mu.Unlock()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			got := b.got()
			if len(got) > 0 && got[len(got)-1] == "up" {
				return // released, as it must be
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("button never released; phases = %v", b.got())
	})

	t.Run("a drag that ends properly is not released twice", func(t *testing.T) {
		b, st := newDragging(), &inputState{}
		applyWindowInput(b, st, windowInput{ID: "w1", Kind: "drag", Phase: "begin"}, nil)
		applyWindowInput(b, st, windowInput{ID: "w1", Kind: "drag", Phase: "end"}, nil)
		st.drag.mu.Lock()
		stillArmed := st.drag.timer != nil
		st.drag.mu.Unlock()
		if stillArmed {
			t.Fatal("watchdog left armed after a clean end")
		}
		time.Sleep(50 * time.Millisecond)
		ups := 0
		for _, p := range b.got() {
			if p == "up" {
				ups++
			}
		}
		if ups != 1 {
			t.Fatalf("got %d releases, want exactly 1", ups)
		}
	})
}
