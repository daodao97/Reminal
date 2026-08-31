// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"testing"
	"time"
)

// sizedAgent is an agent already settled at cols x rows, with no PTY attached:
// applyEffectiveSize is a no-op, so the tests observe exactly what the
// coalescer decided.
func sizedAgent(cols, rows uint16) *Agent {
	return &Agent{
		viewerCount:    1,
		viewerCols:     cols,
		viewerRows:     rows,
		lastAppliedCol: cols,
		lastAppliedRow: rows,
	}
}

// A soft keyboard does not vanish in one frame: as it slides away the viewport
// is reported at a RISING sequence of heights. The settle has to land on the
// height the user is actually left with, not on the smallest frame seen
// mid-animation — landing on the latter is "I collapsed the keyboard and the
// terminal didn't resize".
func TestCoalesceKeyboardCloseSettlesAtFinalHeight(t *testing.T) {
	a := sizedAgent(80, 20)
	for _, rows := range []uint16{22, 28, 40} {
		a.coalesceViewerResize(80, rows)
	}
	time.Sleep(resizeSettle + 150*time.Millisecond)

	a.viewerSizeMu.Lock()
	gotC, gotR := a.viewerCols, a.viewerRows
	a.viewerSizeMu.Unlock()
	if gotC != 80 || gotR != 40 {
		t.Errorf("settled at %dx%d after a keyboard close, want 80x40", gotC, gotR)
	}
}

// The mirror image: the keyboard sliding IN reports a falling sequence, and the
// settle must land on the height left visible above the keyboard.
func TestCoalesceKeyboardOpenSettlesAtFinalHeight(t *testing.T) {
	a := sizedAgent(80, 40)
	for _, rows := range []uint16{32, 24, 20} {
		a.coalesceViewerResize(80, rows)
	}
	time.Sleep(resizeSettle + 150*time.Millisecond)

	a.viewerSizeMu.Lock()
	gotC, gotR := a.viewerCols, a.viewerRows
	a.viewerSizeMu.Unlock()
	if gotC != 80 || gotR != 20 {
		t.Errorf("settled at %dx%d after a keyboard open, want 80x20", gotC, gotR)
	}
}

// Opening and closing inside a single settle window must end up where the user
// ended up, not at the transient low point.
func TestCoalesceOpenThenCloseSettlesAtFinalHeight(t *testing.T) {
	a := sizedAgent(80, 40)
	a.coalesceViewerResize(80, 20)
	a.coalesceViewerResize(80, 40)
	time.Sleep(resizeSettle + 150*time.Millisecond)

	a.viewerSizeMu.Lock()
	gotR := a.viewerRows
	a.viewerSizeMu.Unlock()
	if gotR != 40 {
		t.Errorf("settled at %d rows, want 40 — a transient shrink became the result", gotR)
	}
}

// Address-bar jitter (a couple of rows, same width) must NOT take the fast
// path: on a phone it reverses on the next scroll gesture, and every applied
// change costs the running program a full repaint.
func TestCoalesceAddressBarJitterWaitsForStability(t *testing.T) {
	a := sizedAgent(80, 40)
	a.coalesceViewerResize(80, 42)

	time.Sleep(resizeSettle + 150*time.Millisecond)
	a.viewerSizeMu.Lock()
	early := a.viewerRows
	a.viewerSizeMu.Unlock()
	if early != 40 {
		t.Errorf("jitter grow applied after %v (rows=%d); it should wait for the stability window", resizeSettle, early)
	}

	time.Sleep(resizeGrowStable)
	a.viewerSizeMu.Lock()
	late := a.viewerRows
	a.viewerSizeMu.Unlock()
	if late != 42 {
		t.Errorf("settled jitter grow never applied (rows=%d, want 42)", late)
	}
}
