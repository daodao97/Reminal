// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"errors"
	"testing"

	creackpty "github.com/creack/pty"
	ipty "github.com/reminal/reminal/internal/pty"
)

// The snapshot rebuild must start at the geometry the shell is really rendering
// at. On hot-restart resume no viewer has reported a size yet, so the seed has
// to come from the inherited PTY — falling back to 80x24 there replays the
// pre-first-resize output at the wrong height and stamps duplicated scrollback.
func TestResolveSeedGeometry(t *testing.T) {
	fail := func() (uint16, uint16, error) { return 0, 0, errors.New("no tty") }

	// A viewer-applied size wins over everything.
	if c, r := resolveSeedGeometry(120, 50, func() (uint16, uint16, error) { return 100, 40, nil }); c != 120 || r != 50 {
		t.Errorf("viewer size should win: got %dx%d, want 120x50", c, r)
	}
	// Resume: no viewer size yet -> seed from the PTY (the regression this guards).
	if c, r := resolveSeedGeometry(0, 0, func() (uint16, uint16, error) { return 100, 40, nil }); c != 100 || r != 40 {
		t.Errorf("resume should seed from PTY: got %dx%d, want 100x40", c, r)
	}
	// PTY query fails -> 80x24 fallback.
	if c, r := resolveSeedGeometry(0, 0, fail); c != 80 || r != 24 {
		t.Errorf("fallback on PTY error: got %dx%d, want 80x24", c, r)
	}
	// No PTY at all -> 80x24 fallback.
	if c, r := resolveSeedGeometry(0, 0, nil); c != 80 || r != 24 {
		t.Errorf("fallback with no PTY: got %dx%d, want 80x24", c, r)
	}
}

// End-to-end guard: initScreen on a resumed agent (viewer size unknown) must
// seed the rebuild baseline from the inherited PTY's real size, not 80x24.
func TestInitScreenSeedsGeometryFromPTYOnResume(t *testing.T) {
	ptmx, tty, err := creackpty.Open()
	if err != nil {
		t.Skipf("pty unavailable: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	defer func() { _ = tty.Close() }()
	if err := creackpty.Setsize(ptmx, &creackpty.Winsize{Rows: 40, Cols: 100}); err != nil {
		t.Fatalf("setsize: %v", err)
	}

	a := &Agent{buf: newScrollback(scrollbackBytes), term: ipty.Attach(ptmx)}
	a.initScreen()

	if c, r := a.buf.Base(); c != 100 || r != 40 {
		t.Fatalf("initScreen on resume seeded %dx%d, want 100x40 (regression: fell back to 80x24)", c, r)
	}
}
