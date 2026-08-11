// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"slices"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

// logicalLine is one UN-wrapped line of styled terminal content — a "paragraph"
// the app printed, stored as its raw cells (grapheme + style + display width),
// independent of any wrap column. Re-wrapping ("reflow") to a viewer's width is
// then a lossless re-break of these cells. Storing history this way — instead of
// as pre-wrapped rows baked at one width — is what makes serving different viewer
// widths safe from the scrollback-duplication bug: a frozen logical line, re-broken,
// cannot duplicate.
type logicalLine uv.Line

// trimRow returns a grid row trimmed of trailing blank cells, and whether its
// content reached the terminal's last column — which, by the standard trailing-blank
// reflow heuristic (what xterm/iTerm use), means it soft-wrapped into the next row
// rather than ending on a real newline. `uv.Line` carries no wrap flag, so this is
// how we recover logical lines.
func trimRow(line uv.Line, width int) (cells uv.Line, filled bool) {
	lastContentIdx := -1
	lastContentCol := 0
	col := 0
	for i := range line {
		c := &line[i]
		w := c.Width
		if w <= 0 {
			w = 1
		}
		if c.Content != "" && c.Content != " " && !c.IsZero() {
			lastContentIdx = i
			lastContentCol = col + w
		}
		col += w
	}
	if lastContentIdx < 0 {
		return uv.Line{}, false // blank row => hard newline
	}
	return slices.Clone(line[:lastContentIdx+1]), lastContentCol >= width
}

// extractLogical pulls an emulator's scrollback buffer + current screen into
// logical lines, joining soft-wrapped rows back into one line each.
func extractLogical(e *vt.Emulator) []logicalLine {
	w := e.Width()
	var rows []uv.Line
	if sb := e.Scrollback(); sb != nil {
		for i := 0; i < sb.Len(); i++ {
			rows = append(rows, sb.Line(i))
		}
	}
	for y := 0; y < e.Height(); y++ {
		row := make(uv.Line, 0, w)
		for x := 0; x < w; x++ {
			if c := e.CellAt(x, y); c != nil {
				row = append(row, *c)
			} else {
				row = append(row, uv.EmptyCell)
			}
		}
		rows = append(rows, row)
	}

	var out []logicalLine
	var cur uv.Line
	pending := false
	for _, r := range rows {
		cells, filled := trimRow(r, w)
		cur = append(cur, cells...)
		pending = true
		if !filled {
			out = append(out, logicalLine(cur))
			cur = nil
			pending = false
		}
	}
	if pending {
		out = append(out, logicalLine(cur))
	}
	// Drop trailing blank logical lines (the screen's empty tail below content).
	for len(out) > 0 && len(out[len(out)-1]) == 0 {
		out = out[:len(out)-1]
	}
	return out
}

// reflowRows re-wraps logical lines to the given column width, breaking on display
// columns (wide graphemes stay whole). Pure re-break of frozen cells — it moves
// wrap points but never adds or drops a cell.
func reflowRows(lines []logicalLine, width int) []uv.Line {
	if width < 1 {
		width = 1
	}
	var out []uv.Line
	for _, ll := range lines {
		cells := uv.Line(ll)
		if len(cells) == 0 {
			out = append(out, uv.Line{})
			continue
		}
		var row uv.Line
		col := 0
		for i := range cells {
			cw := cells[i].Width
			if cw <= 0 {
				cw = 1
			}
			if col+cw > width && len(row) > 0 {
				out = append(out, row)
				row = nil
				col = 0
			}
			row = append(row, cells[i])
			col += cw
		}
		out = append(out, row)
	}
	return out
}

// serializeRows renders reflowed rows back to styled ANSI (CRLF-separated) for a
// viewer's terminal. Uses ultraviolet's own line renderer so colours, bold, links,
// etc. survive the re-wrap.
func serializeRows(rows []uv.Line) string {
	var b strings.Builder
	for i, r := range rows {
		b.WriteString(r.Render())
		if i < len(rows)-1 {
			b.WriteString("\r\n")
		}
	}
	return b.String()
}

// reflowSnapshot is the whole pipeline: pull an emulator's history+screen into
// logical lines and render them wrapped to `width` as styled ANSI. This is what a
// viewer at that width receives — dup-free by construction.
func reflowSnapshot(e *vt.Emulator, width int) string {
	return serializeRows(reflowRows(extractLogical(e), width))
}
