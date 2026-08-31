// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"fmt"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

// resizeAnchoredBottom resizes e to (cols, rows) the way a real terminal does:
// anchored to the BOTTOM.
//
// vt.Emulator.Resize is anchored to the top. Growing appends blank rows BELOW
// the content; shrinking truncates the bottom rows away and drops them, not
// even into scrollback. Drag the corner of Terminal.app and neither happens:
// the last line stays where it is relative to the bottom while older lines are
// revealed above it, and shrinking scrolls the top off into scrollback.
//
// The agent's screen is what snapshots are rendered from, so a top-anchored
// resize is what put a dead band under a session's prompt after a phone
// keyboard collapsed (grow), and what silently ate the newest lines of output
// when it opened (shrink). Nothing here knows or cares which program is
// running; it is the same arithmetic for a bare shell and a full-screen TUI.
//
// The alternate screen (vim, htop) is exempt: it has no scrollback and is
// genuinely absolutely addressed, so the app repaints it wholesale and a plain
// resize is already right.
func resizeAnchoredBottom(e *vt.Emulator, cols, rows int) {
	if e == nil || cols <= 0 || rows <= 0 {
		return
	}
	oldRows := e.Height()
	switch {
	case rows == oldRows || e.IsAltScreen():
		e.Resize(cols, rows)
	case rows < oldRows:
		shrinkAnchoredBottom(e, cols, rows, oldRows)
	default:
		growAnchoredBottom(e, cols, rows, oldRows)
	}
}

// shrinkAnchoredBottom scrolls rows off the TOP into scrollback so the bottom
// of the screen survives, then lets Resize drop the rows they vacated.
func shrinkAnchoredBottom(e *vt.Emulator, cols, rows, oldRows int) {
	// Scroll only as far as the bottom-most used row demands. A window holding
	// three lines of output shrinking from 40 rows to 20 scrolls nothing at
	// all — it just loses blank rows, exactly like a real terminal.
	need := lastUsedRow(e) - (rows - 1)
	if need <= 0 {
		e.Resize(cols, rows)
		return
	}
	if need > oldRows {
		need = oldRows
	}
	w := e.Width()
	if sb := e.Scrollback(); sb != nil {
		for y := 0; y < need; y++ {
			sb.Push(rowLine(e, y, w))
		}
	}
	// Slide the survivors up, then blank what they vacated so Resize truncates
	// empty rows instead of a second copy of the content.
	for y := 0; y+need < oldRows; y++ {
		copyRow(e, y+need, y, w)
	}
	for y := oldRows - need; y < oldRows; y++ {
		blankRow(e, y, w)
	}
	cur := e.CursorPosition()
	e.Resize(cols, rows)
	moveCursor(e, cur.X, cur.Y-need, cols, rows)
}

// growAnchoredBottom reveals the newest scrollback lines above the content so
// the bottom of the screen does not move.
func growAnchoredBottom(e *vt.Emulator, cols, rows, oldRows int) {
	sb := e.Scrollback()
	pull := rows - oldRows
	if n := sb.Len(); pull > n {
		pull = n
	}
	cur := e.CursorPosition()
	e.Resize(cols, rows) // blank rows land at the bottom; content stays put
	if pull <= 0 {
		// Nothing in scrollback to reveal, so blank rows below the content is
		// the correct result — same as growing a freshly opened terminal.
		return
	}
	// Take the newest lines back out of scrollback. There is no pop, so keep
	// the remainder and rebuild; lines are re-pushed by reference, so this
	// costs a slice walk, not a copy of the history.
	all := sb.Lines()
	revealed := append(all[:0:0], all[len(all)-pull:]...)
	kept := append(all[:0:0], all[:len(all)-pull]...)
	sb.Clear()
	for _, ln := range kept {
		sb.Push(ln)
	}
	// Slide the content down to make room, then lay the revealed lines above
	// it. The bottom of the screen has not moved.
	w := e.Width()
	for y := rows - 1; y >= pull; y-- {
		copyRow(e, y-pull, y, w)
	}
	for y, ln := range revealed {
		writeRow(e, y, ln, w)
	}
	moveCursor(e, cur.X, cur.Y+pull, cols, rows)
}

// lastUsedRow is the bottom-most row holding content or the cursor: the row a
// terminal has to keep on screen when it shrinks. Blank rows below it are free
// to go, which is why shrinking a mostly-empty window scrolls nothing.
func lastUsedRow(e *vt.Emulator) int {
	last := e.CursorPosition().Y
	for y := e.Height() - 1; y > last; y-- {
		if !rowBlank(e, y) {
			return y
		}
	}
	return last
}

func rowBlank(e *vt.Emulator, y int) bool {
	for x, w := 0, e.Width(); x < w; x++ {
		c := e.CellAt(x, y)
		if c == nil {
			continue
		}
		if !c.IsZero() && !c.Equal(&uv.EmptyCell) {
			return false
		}
	}
	return true
}

// rowLine copies a screen row out as a scrollback line.
func rowLine(e *vt.Emulator, y, w int) uv.Line {
	ln := make(uv.Line, w)
	for x := 0; x < w; x++ {
		if c := e.CellAt(x, y); c != nil {
			ln[x] = *c
			continue
		}
		ln[x] = uv.EmptyCell
	}
	return ln
}

func copyRow(e *vt.Emulator, src, dst, w int) {
	if src == dst {
		return
	}
	for x := 0; x < w; x++ {
		cell := uv.EmptyCell
		if c := e.CellAt(x, src); c != nil {
			cell = *c
		}
		e.SetCell(x, dst, &cell)
	}
}

func blankRow(e *vt.Emulator, y, w int) {
	for x := 0; x < w; x++ {
		cell := uv.EmptyCell
		e.SetCell(x, y, &cell)
	}
}

// writeRow lays a scrollback line onto a screen row. Scrollback lines are
// stored with trailing blanks trimmed, so anything past the line is blanked.
func writeRow(e *vt.Emulator, y int, ln uv.Line, w int) {
	for x := 0; x < w; x++ {
		cell := uv.EmptyCell
		if x < len(ln) {
			cell = ln[x]
		}
		e.SetCell(x, y, &cell)
	}
}

// moveCursor repositions the cursor. vt only exposes cursor movement through
// the parser, and Resize has just reset the scroll region to the whole screen,
// so an absolute CUP lands exactly where we ask.
func moveCursor(e *vt.Emulator, x, y, cols, rows int) {
	if y < 0 {
		y = 0
	}
	if y > rows-1 {
		y = rows - 1
	}
	if x < 0 {
		x = 0
	}
	if x > cols-1 {
		x = cols - 1
	}
	_, _ = e.Write([]byte(fmt.Sprintf("\x1b[%d;%dH", y+1, x+1)))
}
