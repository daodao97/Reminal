// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package main

import "testing"

func TestParseExposeArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		port    int
		public  bool
		label   string
		wantErr bool
	}{
		{"just a port", []string{"8080"}, 8080, false, "", false},
		{"public", []string{"8080", "--public"}, 8080, true, "", false},
		// The thing this exists for: a name with spaces, taken whole.
		{"a name in two words", []string{"8080", "--name", "Quarterly report"}, 8080, false, "Quarterly report", false},
		{"the equals form", []string{"8080", "--name=Quarterly report"}, 8080, false, "Quarterly report", false},
		{"name before the port", []string{"--name", "Staging", "3000"}, 3000, false, "Staging", false},
		{"name and public together", []string{"443", "--public", "--name", "Docs"}, 443, true, "Docs", false},
		{"a name that looks like a flag", []string{"80", "--name", "--public"}, 80, false, "--public", false},

		{"no port", []string{"--name", "x"}, 0, false, "", true},
		{"a name with no name", []string{"8080", "--name"}, 0, false, "", true},
		// Sscanf's %d stopped at the first non-digit and reported success, so
		// a mistyped port quietly forwarded a different one.
		{"a port with a typo in it", []string{"8080abc"}, 0, false, "", true},
		{"port out of range", []string{"70000"}, 0, false, "", true},
		{"zero", []string{"0"}, 0, false, "", true},
		{"two ports", []string{"80", "443"}, 0, false, "", true},
		{"an option nobody has", []string{"80", "--quiet"}, 0, false, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			port, public, label, err := parseExposeArgs(c.args)
			if (err != nil) != c.wantErr {
				t.Fatalf("parseExposeArgs(%q) err = %v, wantErr %v", c.args, err, c.wantErr)
			}
			if c.wantErr {
				return
			}
			if port != c.port || public != c.public || label != c.label {
				t.Fatalf("parseExposeArgs(%q) = (%d, %v, %q), want (%d, %v, %q)",
					c.args, port, public, label, c.port, c.public, c.label)
			}
		})
	}
}
