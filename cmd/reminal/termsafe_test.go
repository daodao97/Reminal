// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package main

import (
	"strings"
	"testing"
)

// What `reminal issues` prints is text only: an agent's report cannot
// rewrite the screen, retitle the window, or put a command on the clipboard.
func TestTermSafeLeavesOnlyText(t *testing.T) {
	c := func(r ...rune) string { return string(r) }
	esc, bel, csi1, del, rlo := c(0x1b), c(0x07), c(0x9b), c(0x7f), c(0x202e)
	in := "keys\tdid not land\n" + esc + "]52;c;cm0gLXJmIH4=" + bel + "note" + csi1 + "2J" + del + rlo + "txt.exe\n"
	got := termSafe(in)
	for _, bad := range []string{esc, bel, csi1, del, rlo} {
		if strings.Contains(got, bad) {
			t.Fatalf("kept %q: %q", bad, got)
		}
	}
	if !strings.HasPrefix(got, "keys\tdid not land\n") || !strings.HasSuffix(got, "txt.exe\n") {
		t.Fatalf("lines and tabs should survive: %q", got)
	}
}
