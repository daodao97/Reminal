// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import "testing"

func TestTypedByPerson(t *testing.T) {
	for _, c := range []struct {
		in     string
		person bool
	}{
		{"ls\r", true},
		{"\x1b[A", true},                            // arrow up
		{"\x1b[200~text\x1b[201~", true},            // a paste
		{"\x1b[<0;10;5M", true},                     // a mouse click
		{"\x1b[12;40R", false},                      // cursor position report
		{"\x1b[?62;22c", false},                     // device attributes
		{"\x1b[>1;10;0c", false},                    // secondary device attributes
		{"\x1b[0n", false},                          // status ok
		{"\x1b[I", false},                           // focus in
		{"\x1b[O", false},                           // focus out
		{"\x1b]11;rgb:1111/1111/1111\x1b\\", false}, // background colour reply
		{"\x1b]10;rgb:ffff/ffff/ffff\x07", false},
		{"\x1bP>|xterm.js(5.5)\x1b\\", false}, // version reply
		{"\x1b[12;40Rx", true},                // a reply and a key
	} {
		if got := typedByPerson([]byte(c.in)); got != c.person {
			t.Errorf("%q: person %v, want %v", c.in, got, c.person)
		}
	}
}

// noteInput is the only way either input path records typing, so this is the
// test that would have caught a merge leaving a bare markInput beside the
// guarded one — the shape that silently undoes typedByPerson.
func TestNoteInputIgnoresTheTerminalsOwnReplies(t *testing.T) {
	cases := []struct {
		name  string
		data  string
		typed bool
	}{
		{"a keystroke", "x", true},
		{"a carriage return", "\r", true},
		{"cursor position report", "\x1b[24;80R", false},
		{"device attributes", "\x1b[?62;c", false},
		{"device status report", "\x1b[0n", false},
		{"focus in", "\x1b[I", false},
		{"focus out", "\x1b[O", false},
		{"an OSC colour reply", "\x1b]11;rgb:1e1e/1e1e/1e1e\x07", false},
		{"a reply with a keystroke after it", "\x1b[24;80Rq", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &Agent{}
			a.noteInput([]byte(c.data))
			moved := !a.lastInput.IsZero()
			if moved != c.typed {
				t.Fatalf("noteInput(%q) recorded typing = %v, want %v", c.data, moved, c.typed)
			}
		})
	}
}
