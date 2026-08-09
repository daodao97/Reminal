// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/x/vt"
)

func newVView(t *testing.T, cols, rows int) (*vt.Emulator, *vviewWriter) {
	t.Helper()
	e := vt.NewEmulator(cols, 400)
	go func() { _, _ = io.Copy(io.Discard, e) }()
	return e, &vviewWriter{e: e, rows: rows}
}

func vviewRows(e *vt.Emulator) []string {
	lines := strings.Split(e.Render(), "\n")
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	lines = lines[:end]
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return lines
}

// A repaint that homes to "row 1" must land on its own previous render, not on
// absolute row 1 of the tall emulator (which is old history).
func TestVViewRepaintOverwritesInPlace(t *testing.T) {
	e, w := newVView(t, 80, 10)
	// Flow 20 lines: content bottom row 19, viewport [10, 19].
	for i := 1; i <= 20; i++ {
		w.Write([]byte(fmt.Sprintf("line-%02d\r\n", i)))
	}
	// Full-viewport repaint the way inline TUIs do it: home, erase each row,
	// home, rewrite. The rewrite paints the same lines the viewport held.
	w.Write([]byte("\x1b[H"))
	for i := 0; i < 10; i++ {
		w.Write([]byte("\x1b[2K\x1b[1B"))
	}
	w.Write([]byte("\x1b[H"))
	for i := 12; i <= 20; i++ {
		w.Write([]byte(fmt.Sprintf("line-%02d\r\n", i)))
	}
	rows := vviewRows(e)
	counts := map[string]int{}
	for _, r := range rows {
		if strings.HasPrefix(r, "line-") {
			counts[r]++
		}
	}
	for l, n := range counts {
		if n > 1 {
			t.Errorf("%s appears %d times after repaint", l, n)
		}
	}
	if len(counts) != 20 {
		t.Errorf("expected 20 distinct lines, got %d", len(counts))
	}
}

// CUP to the bottom row (status lines, toasts) cannot scroll a real terminal,
// so it must not slide the virtual viewport either.
func TestVViewBottomRowCUPDoesNotSlideViewport(t *testing.T) {
	e, w := newVView(t, 80, 10)
	for i := 1; i <= 20; i++ {
		w.Write([]byte(fmt.Sprintf("line-%02d\r\n", i)))
	}
	base := w.Base()
	// A toast repeatedly drawn on the viewport's bottom row.
	for i := 0; i < 5; i++ {
		w.Write([]byte("\x1b[10;1Htoast!\x1b[K"))
	}
	if got := w.Base(); got != base {
		t.Errorf("bottom-row CUP slid the viewport: base %d -> %d", base, got)
	}
	_ = e
}

// Height changes re-anchor the viewport BOTTOM: shrinking pushes the top down,
// growing reveals older rows above (what a bottom-anchored terminal shows).
func TestVViewResizeAnchorsBottom(t *testing.T) {
	_, w := newVView(t, 80, 10)
	for i := 1; i <= 30; i++ {
		w.Write([]byte(fmt.Sprintf("line-%02d\r\n", i)))
	}
	// Content occupies rows 0..30 (30 lines + cursor line); viewport bottom 30.
	base10 := w.Base()
	w.setRows(4)
	if got := w.Base(); got != base10+6 {
		t.Errorf("shrink 10->4: base %d, want %d", got, base10+6)
	}
	w.setRows(20)
	if got := w.Base(); got != base10-10 {
		t.Errorf("grow 4->20: base %d, want %d", got, base10-10)
	}
}

// While the app is on the alt screen its absolute addressing is real; the
// translator must stand down and leave the main-buffer content untouched.
func TestVViewAltScreenStanddown(t *testing.T) {
	e, w := newVView(t, 80, 10)
	for i := 1; i <= 15; i++ {
		w.Write([]byte(fmt.Sprintf("line-%02d\r\n", i)))
	}
	w.Write([]byte("\x1b[?1049h\x1b[H\x1b[2Jfullscreen-app\x1b[5;1Hrow5"))
	if !w.alt {
		t.Fatal("alt not detected")
	}
	w.Write([]byte("\x1b[?1049l"))
	if w.alt {
		t.Fatal("alt exit not detected")
	}
	rows := vviewRows(e)
	for i := 1; i <= 15; i++ {
		want := fmt.Sprintf("line-%02d", i)
		found := false
		for _, r := range rows {
			if r == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("main-buffer %s lost across alt screen", want)
		}
	}
}

// ED2 must clear only the virtual viewport, leaving history rows intact.
func TestVViewED2ClearsViewportOnly(t *testing.T) {
	e, w := newVView(t, 80, 10)
	for i := 1; i <= 20; i++ {
		w.Write([]byte(fmt.Sprintf("line-%02d\r\n", i)))
	}
	w.Write([]byte("\x1b[2J"))
	rows := vviewRows(e)
	for i := 1; i <= 10; i++ {
		want := fmt.Sprintf("line-%02d", i)
		found := false
		for _, r := range rows {
			if r == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("history row %s destroyed by ED2", want)
		}
	}
	for i := 12; i <= 20; i++ {
		stale := fmt.Sprintf("line-%02d", i)
		for _, r := range rows {
			if r == stale {
				t.Errorf("viewport row %s survived ED2", stale)
			}
		}
	}
}

// Longer blank runs in rebuilt history are squeezed to one line (resize-race
// artifacts); single blank separators are preserved.
func TestRebuildHistorySqueezesBlankRuns(t *testing.T) {
	in := []string{"a", "", "b", "", "", "", "", "c", "", "d", "", ""}
	squeezed, blanks := in[:0], 0
	for _, ln := range in {
		if strings.TrimSpace(ln) == "" {
			blanks++
			if blanks > 1 {
				continue
			}
		} else {
			blanks = 0
		}
		squeezed = append(squeezed, ln)
	}
	want := []string{"a", "", "b", "", "c", "", "d", ""}
	if len(squeezed) != len(want) {
		t.Fatalf("got %v want %v", squeezed, want)
	}
	for i := range want {
		if squeezed[i] != want[i] {
			t.Fatalf("got %v want %v", squeezed, want)
		}
	}
}

// A width change adopted by an Ink-style TUI (Claude Code et al) must not stamp
// a duplicate copy of the frame into history. Ink's post-SIGWINCH repaint is
// cursor-up over its previous frame + ED mode 0 (erase below) + reprint — it
// never emits the CUP-home/ED2/DECSTBM signatures that applyPending previously
// keyed on, so the new-width reprint was translated at the OLD width and
// mis-anchored: the next frame's cursor-up (counted at the new width) fell
// short, stranding the top of the narrow render above the new one — exactly the
// "same text twice, wrapped at two different widths" reconnect bug.
func TestVViewInkResizeRepaintNoDuplicate(t *testing.T) {
	e, w := newVView(t, 40, 10)
	w.tall = 400
	line := func(i int) string { return fmt.Sprintf("item-%d %s", i, strings.Repeat("x", 52)) }
	frame := func() string {
		var b strings.Builder
		for i := 1; i <= 4; i++ {
			b.WriteString(line(i) + "\r\n")
		}
		return b.String()
	}
	// Initial frame at width 40: each 59-char line hard-wraps into 2 rows → 8 rows.
	w.Write([]byte(frame()))
	// PTY resized to 80 columns; the marker arms the change.
	w.setGeometry(80, 10)
	// The app's post-SIGWINCH repaint (Ink): cursor-up over the previous frame
	// (8 rows, as rendered at the old width) + erase-below + reprint.
	w.Write([]byte("\x1b[8A\x1b[J" + frame()))
	// Next frame tick: Ink now believes the frame is 4 rows tall (new width).
	w.Write([]byte("\x1b[4A\x1b[J" + frame()))

	if got := e.Width(); got != 80 {
		t.Errorf("width change never applied: emulator still %d cols", got)
	}
	counts := map[string]int{}
	for _, r := range vviewRows(e) {
		for i := 1; i <= 4; i++ {
			if strings.HasPrefix(r, fmt.Sprintf("item-%d ", i)) {
				counts[fmt.Sprintf("item-%d", i)]++
			}
		}
	}
	for i := 1; i <= 4; i++ {
		if n := counts[fmt.Sprintf("item-%d", i)]; n != 1 {
			t.Errorf("item-%d appears %d times, want exactly 1 (duplicate stamped into history)\n%s",
				i, n, strings.Join(vviewRows(e), "\n"))
		}
	}
}
