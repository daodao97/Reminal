// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package main

import (
	"strings"
	"testing"
)

func TestWrapText(t *testing.T) {
	// Never exceeds the width and never drops or splits a word.
	got := wrapText("alpha beta gamma delta epsilon", 12)
	for _, ln := range got {
		if len([]rune(ln)) > 12 {
			t.Fatalf("line %q exceeds width 12", ln)
		}
	}
	if strings.Join(got, " ") != "alpha beta gamma delta epsilon" {
		t.Fatalf("words lost or reordered: %v", got)
	}
	// A single over-long word is kept whole on its own line, not truncated.
	if l := wrapText("supercalifragilistic", 8); len(l) != 1 || l[0] != "supercalifragilistic" {
		t.Fatalf("long word should stay whole: %v", l)
	}
	// Empty input yields exactly one empty line (callers print it safely).
	if l := wrapText("", 10); len(l) != 1 || l[0] != "" {
		t.Fatalf("empty input: %v", l)
	}
}
