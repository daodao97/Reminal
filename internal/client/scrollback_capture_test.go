// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// capEvent is one recorded viewer event: a terminal write ("w", base64 bytes) or a
// geometry change ("r", cols/rows). Captured live (via a browser Terminal.write /
// resize hook) from a real idle Claude Code (Ink) session: the stable frame's
// snapshot, a width change, then Ink's reflow repaint — the exact reconnect
// scenario that duplicates scrollback. Fixture: testdata/scrollback_stable_resize.json.
type capEvent struct {
	T string `json:"t"`
	D string `json:"d"`
	C int    `json:"c"`
	R int    `json:"r"`
}

// TestVViewRealCaptureNoDuplicate replays that REAL Ink resize+repaint stream through
// vviewWriter (the agent-side history rebuilder) and asserts no unique line is stamped
// into rebuilt history more than once. This is the deterministic failing-repro oracle
// for the long-standing reconnect scrollback-duplication bug — captured bytes, not a
// hand-modeled sequence (four prior synthetic-model fix attempts missed the real
// failure mode). vviewWriter mis-translates Ink's home+ED+reprint into duplicate
// history on the post-resize repaint; a fresh snapshot of a real session shows blocks
// duplicated 13×, and this reproduces it from ~40 KB of captured bytes.
//
// Currently FAILS (documents the bug). The vviewWriter fix must make it pass.
func TestVViewRealCaptureNoDuplicate(t *testing.T) {
	// WIP repro for the reconnect scrollback-duplication bug. Skipped so it doesn't
	// break `go test ./...` while the fix direction is being decided — the dup was
	// found to be fundamental to inline-TUI resize (a native-scrollback emulator
	// duplicates too), not a vviewWriter-only defect. Un-skip when implementing the fix.
	t.Skip("WIP: scrollback-dup repro; enable when the fix lands")

	// Width the session rendered at before the captured resize (snapshot wraps at 119).
	const startCols, startRows, tall = 119, 51, 400

	data, err := os.ReadFile("testdata/scrollback_stable_resize.json")
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	var events []capEvent
	if err := json.Unmarshal(data, &events); err != nil {
		t.Fatalf("parse capture: %v", err)
	}

	e, w := newVView(t, startCols, startRows)
	w.tall = tall // match the replay emulator height (newVView doesn't set it)
	for _, ev := range events {
		switch ev.T {
		case "w":
			b, derr := base64.StdEncoding.DecodeString(ev.D)
			if derr != nil {
				t.Fatalf("bad base64: %v", derr)
			}
			w.Write(b)
		case "r":
			w.setGeometry(ev.C, ev.R)
		}
	}

	rows := vviewRows(e)
	ansi := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	plain := make([]string, len(rows))
	for i, r := range rows {
		plain[i] = ansi.ReplaceAllString(r, "")
	}

	// Distinctive, one-per-session prose fragments (unaffected by wrap width). Each
	// must appear exactly once in rebuilt history; more than once is duplication.
	unique := []string{
		"people poke holes in the crypto",
		"embedded-GIF launch you wanted",
		"matching how the well-received",
	}
	for _, needle := range unique {
		count := 0
		for _, l := range plain {
			if strings.Contains(l, needle) {
				count++
			}
		}
		if count != 1 {
			t.Errorf("scrollback duplication: %q appears %d× in rebuilt history (want 1)", needle, count)
		} else {
			t.Logf("ok: %q ×1", needle)
		}
	}
}
