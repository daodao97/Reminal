// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package main

import "testing"

func TestParseAddOwnerArgs(t *testing.T) {
	cases := []struct {
		name      string
		in        []string
		wantID    string
		wantLabel string
	}{
		{"bare id", []string{"rmnl_X"}, "rmnl_X", ""},
		{"--label", []string{"rmnl_X", "--label", "My Phone"}, "rmnl_X", "My Phone"},
		{"--label=", []string{"rmnl_X", "--label=Work"}, "rmnl_X", "Work"},
		{"bare label after id", []string{"rmnl_X", "My", "Phone"}, "rmnl_X", "My Phone"},
		{"--label wins over trailing", []string{"rmnl_X", "extra", "--label", "Foo"}, "rmnl_X", "Foo"},
		// A stray paste where the id isn't first: pick the id, but DON'T scavenge
		// the leading words into a label.
		{"id not first → no scavenged label", []string{"My", "Phone", "rmnl_X"}, "rmnl_X", ""},
		{"whole pasted line → id only", []string{"sudo", "reminal", "add", "owner", "rmnl_X"}, "rmnl_X", ""},
		{"no id", []string{"--label", "Foo"}, "", "Foo"},
		{"empty", nil, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id, label := parseAddOwnerArgs(c.in)
			if id != c.wantID || label != c.wantLabel {
				t.Errorf("parseAddOwnerArgs(%q) = (%q, %q), want (%q, %q)",
					c.in, id, label, c.wantID, c.wantLabel)
			}
		})
	}
}
