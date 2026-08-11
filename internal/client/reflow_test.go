// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

func feedFixtureSingleWidth(t *testing.T, cols, rows int) *vt.Emulator {
	t.Helper()
	data, err := os.ReadFile("testdata/scrollback_stable_resize.json")
	if err != nil {
		t.Fatal(err)
	}
	var events []capEvent
	if err := json.Unmarshal(data, &events); err != nil {
		t.Fatal(err)
	}
	e := vt.NewEmulator(cols, rows)
	e.Scrollback().SetMaxLines(20000)
	for _, ev := range events {
		if ev.T == "r" { // pinned width: stop before the resize
			break
		}
		b, _ := base64.StdEncoding.DecodeString(ev.D)
		e.Write(b)
	}
	return e
}

func nonSpaceOfLogical(lls []logicalLine) int {
	n := 0
	for _, ll := range lls {
		for _, c := range uv.Line(ll) {
			if c.Content != "" && c.Content != " " {
				for _, r := range c.Content {
					_ = r
					n++
				}
			}
		}
	}
	return n
}

func nonSpaceOfText(s string) int {
	n := 0
	for _, r := range s {
		if r != ' ' && r != '\r' && r != '\n' {
			n++
		}
	}
	return n
}

// TestReflowLosslessAndStyled proves the production reflow on REAL content:
//  1. re-wrapping to any width preserves the character count (no dup / no loss),
//  2. the serialized output keeps styling (SGR present),
//  3. full round-trip (extract → reflow(W) → serialize → re-parse at W) preserves
//     content — so the pipeline a viewer actually uses can't duplicate.
func TestReflowLosslessAndStyled(t *testing.T) {
	e := feedFixtureSingleWidth(t, 119, 51)
	logical := extractLogical(e)
	base := nonSpaceOfLogical(logical)
	t.Logf("logical lines=%d  non-space chars=%d", len(logical), base)
	if base < 1000 {
		t.Fatalf("suspiciously little content extracted (%d chars)", base)
	}

	for _, w := range []int{20, 40, 58, 90, 119, 200} {
		rows := reflowRows(logical, w)
		// (1) no cell added or dropped by the re-wrap
		got := 0
		for _, r := range rows {
			got += nonSpaceOfLogical([]logicalLine{logicalLine(r)})
		}
		if got != base {
			t.Errorf("reflowRows@%d changed content: %d chars, want %d", w, got, base)
		}
		// (2) serialize keeps styling
		ansi := serializeRows(rows)
		if !strings.Contains(ansi, "\x1b[") {
			t.Errorf("reflow@%d produced no SGR — styling lost", w)
		}
		// (3) round-trip through a fresh emulator at width w
		e2 := vt.NewEmulator(w, 51)
		e2.Scrollback().SetMaxLines(20000)
		e2.Write([]byte(ansi))
		rt := nonSpaceOfLogical(extractLogical(e2))
		if rt != base {
			t.Errorf("round-trip@%d changed content: %d chars, want %d", w, rt, base)
		}
		t.Logf("reflow@%3d: rows=%d chars=%d roundtrip=%d", w, len(rows), got, rt)
	}
}
