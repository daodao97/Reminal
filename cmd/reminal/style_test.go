package main

import "testing"

func TestCleanTerm(t *testing.T) {
	cases := map[string]string{
		"safe-title":         "safe-title",
		"\x1b[31mred\x1b[0m": "[31mred[0m", // ESC dropped; the rest is inert literal text
		"\x1b]0;pwn\x07":     "]0;pwn",     // OSC: ESC + BEL dropped
		"a\x07b\x00c\x7fd":   "abcd",       // BEL, NUL, DEL dropped
		"line1\nline2":       "line1line2", // newline (C0) dropped so it can't spoof rows
		"tab\there":          "tabhere",
	}
	for in, want := range cases {
		if got := cleanTerm(in); got != want {
			t.Errorf("cleanTerm(%q) = %q, want %q", in, got, want)
		}
	}
}
