// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import "testing"

// What a TUI spaces with cursor moves is read with its spaces — an agent
// searching a transcript for "trust this folder" must find it.
func TestStripANSIKeepsCursorForwardAsSpaces(t *testing.T) {
	cases := map[string]string{
		"Yes,\x1b[1CI\x1b[1Ctrust\x1b[Cthis\x1b[3Cfolder": "Yes, I trust this   folder",
		"\x1b[1mbold\x1b[0m and \x1b[32mgreen\x1b[m":      "bold and green",
		"a\x1b[99999Cb": "a" + string(make([]byte, 0)) + spaces(256) + "b",
		"x\x1b[2Ky\r\n": "xy\n",
	}
	for in, want := range cases {
		if got := stripANSI(in); got != want {
			t.Errorf("%q:\n got %q\nwant %q", in, got, want)
		}
	}
}

func spaces(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}
