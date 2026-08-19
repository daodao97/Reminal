// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package pty

import (
	"testing"
	"time"
)

var dsrEpoch = time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)

func TestDSRResponderAnswersOnce(t *testing.T) {
	d := newDSRResponder(30, 1, dsrEpoch)

	out, reply := d.filter([]byte("hello\x1b[6nworld"), dsrEpoch)
	if string(out) != "helloworld" {
		t.Errorf("query not removed from the stream: %q", out)
	}
	if string(reply) != "\x1b[30;1R" {
		t.Errorf("reply = %q, want the cursor position report", reply)
	}

	// Every later query belongs to the app running in the shell — a full-screen
	// program asking where the cursor is. It must reach the real terminal.
	out, reply = d.filter([]byte("later\x1b[6n"), dsrEpoch)
	if string(out) != "later\x1b[6n" {
		t.Errorf("a later query was swallowed: %q", out)
	}
	if reply != nil {
		t.Errorf("answered twice: %q", reply)
	}
}

func TestDSRResponderHandlesSplitQuery(t *testing.T) {
	for _, split := range [][]string{
		{"a\x1b", "[6nb"},
		{"a\x1b[", "6nb"},
		{"a\x1b[6", "nb"},
		{"a", "\x1b", "[", "6", "n", "b"},
	} {
		d := newDSRResponder(12, 5, dsrEpoch)
		var got, replies string
		for _, chunk := range split {
			out, reply := d.filter([]byte(chunk), dsrEpoch)
			got += string(out)
			replies += string(reply)
		}
		if got != "ab" {
			t.Errorf("split %q: forwarded %q, want \"ab\"", split, got)
		}
		if replies != "\x1b[12;5R" {
			t.Errorf("split %q: reply %q", split, replies)
		}
	}
}

func TestDSRResponderReleasesFalsePrefix(t *testing.T) {
	d := newDSRResponder(3, 4, dsrEpoch)
	out, _ := d.filter([]byte("x\x1b["), dsrEpoch)
	if string(out) != "x" {
		t.Errorf("prefix not held back: %q", out)
	}
	out, reply := d.filter([]byte("2Jy"), dsrEpoch)
	if string(out) != "\x1b[2Jy" {
		t.Errorf("held-back bytes lost or reordered: %q", out)
	}
	if reply != nil {
		t.Errorf("unexpected reply: %q", reply)
	}
}

// A console that never asks must not leave bytes stranded in the responder, and
// must not keep scanning the stream forever.
func TestDSRResponderStandsDownAfterWindow(t *testing.T) {
	d := newDSRResponder(9, 9, dsrEpoch)
	if out, _ := d.filter([]byte("tail\x1b["), dsrEpoch); string(out) != "tail" {
		t.Fatalf("setup: %q", out)
	}
	late := dsrEpoch.Add(cursorInheritWindow + time.Second)
	out, reply := d.filter([]byte("more"), late)
	if string(out) != "\x1b[more" {
		t.Errorf("held-back bytes not released on timeout: %q", out)
	}
	if reply != nil {
		t.Errorf("replied after the window closed: %q", reply)
	}
	if out, _ := d.filter([]byte("\x1b[6n"), late); string(out) != "\x1b[6n" {
		t.Errorf("still filtering after standing down: %q", out)
	}
}

func TestDSRResponderNilWithoutPosition(t *testing.T) {
	if d := newDSRResponder(0, 1, dsrEpoch); d != nil {
		t.Error("a responder was built without a cursor position to report")
	}
	var d *dsrResponder
	out, reply := d.filter([]byte("\x1b[6n"), dsrEpoch)
	if string(out) != "\x1b[6n" || reply != nil {
		t.Errorf("nil responder must pass everything through: %q %q", out, reply)
	}
}
