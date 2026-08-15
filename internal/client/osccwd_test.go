// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import "testing"

// The OSC 7 emitters are shell prompt hooks with best-effort escaping — the
// parser must take clean URIs, unescaped spaces, percent-encoding, and the
// Windows drive-letter form alike.
func TestParseOSC7(t *testing.T) {
	cases := map[string]string{
		"file://mac.local/Users/me/project": "/Users/me/project",
		"file:///Users/me/my project":       "/Users/me/my project",
		"file://host/Users/me/my%20project": "/Users/me/my project",
		"file:///C:/Users/me":               "C:/Users/me",
		"file://":                           "",
		"http://example.com/x":              "",
		"":                                  "",
	}
	for in, want := range cases {
		if got := parseOSC7(in); got != want {
			t.Errorf("parseOSC7(%q) = %q, want %q", in, got, want)
		}
	}
}
