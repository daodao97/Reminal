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
		wantYes   bool
	}{
		{"bare id", []string{"rmnl_X"}, "rmnl_X", "", false},
		{"--label", []string{"rmnl_X", "--label", "My Phone"}, "rmnl_X", "My Phone", false},
		{"--label=", []string{"rmnl_X", "--label=Work"}, "rmnl_X", "Work", false},
		{"bare label after id", []string{"rmnl_X", "My", "Phone"}, "rmnl_X", "My Phone", false},
		{"--label wins over trailing", []string{"rmnl_X", "extra", "--label", "Foo"}, "rmnl_X", "Foo", false},
		// A stray paste where the id isn't first: pick the id, but DON'T scavenge
		// the leading words into a label.
		{"id not first → no scavenged label", []string{"My", "Phone", "rmnl_X"}, "rmnl_X", "", false},
		{"whole pasted line → id only", []string{"sudo", "reminal", "add", "owner", "rmnl_X"}, "rmnl_X", "", false},
		{"no id", []string{"--label", "Foo"}, "", "Foo", false},
		{"empty", nil, "", "", false},
		{"-y", []string{"rmnl_X", "-y"}, "rmnl_X", "", true},
		{"--yes", []string{"--yes", "rmnl_X"}, "rmnl_X", "", true},
		// The sudo re-exec appends -y AFTER whatever the user typed — it must
		// not disturb the bare-label capture.
		{"-y appended after bare label", []string{"rmnl_X", "My", "Phone", "-y"}, "rmnl_X", "My Phone", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id, label, yes := parseAddOwnerArgs(c.in)
			if id != c.wantID || label != c.wantLabel || yes != c.wantYes {
				t.Errorf("parseAddOwnerArgs(%q) = (%q, %q, %v), want (%q, %q, %v)",
					c.in, id, label, yes, c.wantID, c.wantLabel, c.wantYes)
			}
		})
	}
}
