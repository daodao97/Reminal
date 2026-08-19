// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package pty

import (
	"bytes"
	"fmt"
	"time"
)

// HolderOpts is what the agent measured about the console it is running in,
// for the detached holder that cannot measure it itself. Zero values mean
// "unknown": the pseudo console falls back to 80x24 and inherits no cursor.
type HolderOpts struct {
	Cols, Rows uint16 // size to build the pseudo console at
	// CursorRow/CursorCol is where the host console's cursor sat after the
	// banner was printed, 1-based and window-relative — the position the
	// console's start-up query is answered with, and the row the shell will
	// start on. Present only when the launcher had a console.
	CursorRow, CursorCol uint16
}

// dsrCPR is DSR 6 — "report the cursor position". A pseudo console created to
// INHERIT the terminal's cursor asks this exactly once, at start-up, and waits
// for the answer before it paints anything.
var dsrCPR = []byte("\x1b[6n")

// cursorInheritWindow bounds how long we stay armed. The query is emitted
// during console start-up; anything later belongs to the app running in the
// shell (`claude` and friends ask for the cursor constantly) and must reach the
// real terminal untouched.
const cursorInheritWindow = 5 * time.Second

// dsrResponder answers the ONE cursor-position query a pseudo console asks when
// it was created with PSEUDOCONSOLE_INHERIT_CURSOR, and hides that query from
// everyone downstream.
//
// Both halves matter. Answering is what stops the screen being wiped: conhost
// only skips its first-paint "erase the whole viewport" once a reply arrives
// (VtEngine::InheritCursor — "Prevent us from clearing the entire viewport on
// the first paint" — sets _firstPaint = false), and the reply is also what
// anchors the console's output BELOW whatever the terminal already shows, so
// the shell starts under reminal's banner instead of on top of it. With no
// answer the flag achieves nothing at all.
//
// Hiding it is what keeps the answer singular. The query would otherwise be
// mirrored to the user's real terminal, which answers too — a second report
// arriving after the console stopped listening, landing in the shell's input
// as a stray "[30;1R" at the prompt.
type dsrResponder struct {
	row, col uint16 // the position to report (1-based, viewport-relative)

	armed    bool
	deadline time.Time
	pending  []byte // trailing partial query held across chunk boundaries
}

// newDSRResponder returns a responder for the given host cursor position, or
// nil when there is no position to inherit (headless sessions, or a launcher
// with no console — those don't set the inherit flag either).
func newDSRResponder(row, col uint16, now time.Time) *dsrResponder {
	if row == 0 || col == 0 {
		return nil
	}
	return &dsrResponder{row: row, col: col, armed: true, deadline: now.Add(cursorInheritWindow)}
}

// filter returns the bytes to forward downstream and, when the query was found,
// the reply to write back into the console's input. p is never modified.
func (d *dsrResponder) filter(p []byte, now time.Time) (out, reply []byte) {
	if d == nil || !d.armed {
		return p, nil
	}
	if now.After(d.deadline) {
		// Timed out: release anything held back and stand down for good.
		d.armed = false
		if len(d.pending) > 0 {
			out = append(append([]byte(nil), d.pending...), p...)
			d.pending = nil
			return out, nil
		}
		return p, nil
	}
	if len(d.pending) > 0 {
		joined := make([]byte, 0, len(d.pending)+len(p))
		joined = append(joined, d.pending...)
		joined = append(joined, p...)
		d.pending = nil
		p = joined
	}
	if i := bytes.Index(p, dsrCPR); i >= 0 {
		d.armed = false // answered once; every later query is the app's own
		out = make([]byte, 0, len(p)-len(dsrCPR))
		out = append(out, p[:i]...)
		out = append(out, p[i+len(dsrCPR):]...)
		return out, []byte(fmt.Sprintf("\x1b[%d;%dR", d.row, d.col))
	}
	if n := trailingSeqPrefix(p, dsrCPR); n > 0 {
		d.pending = append(d.pending, p[len(p)-n:]...)
		p = p[:len(p)-n]
	}
	return p, nil
}

// trailingSeqPrefix returns how many bytes at the end of p form a proper prefix
// of seq — the tail that must be held back in case the next read completes it.
func trailingSeqPrefix(p, seq []byte) int {
	n := len(seq) - 1
	if n > len(p) {
		n = len(p)
	}
	for ; n > 0; n-- {
		if bytes.HasSuffix(p, seq[:n]) {
			return n
		}
	}
	return 0
}
