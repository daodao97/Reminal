// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"bytes"
	"testing"
)

// forceStrip drives the filter's stripping logic regardless of the platform the
// tests run on: the behaviour it guards is Windows-only, but the code must be
// verified everywhere it is compiled.
func forceStrip(m *hostMirror, chunks ...string) string {
	var got bytes.Buffer
	for _, c := range chunks {
		p := []byte(c)
		orig := append([]byte(nil), p...)
		out := m.stripSequences(p)
		got.Write(out)
		if !bytes.Equal(p, orig) {
			panic("hostMirror modified the caller's buffer")
		}
	}
	return got.String()
}

func TestHostMirrorDropsHostDamagingSequences(t *testing.T) {
	cases := []struct {
		name   string
		chunks []string
		want   string
	}{
		{
			// conhost's PowerShell shim, verbatim: home, clear screen, clear
			// scrollback. Only the last part is dropped.
			name:   "powershell shim clear",
			chunks: []string{"before\x1b[H\x1b[2J\x1b[3Jafter"},
			want:   "before\x1b[H\x1b[2Jafter",
		},
		{
			name:   "nothing to strip passes through",
			chunks: []string{"plain \x1b[31mred\x1b[0m output\r\n"},
			want:   "plain \x1b[31mred\x1b[0m output\r\n",
		},
		{
			name:   "repeated clears",
			chunks: []string{"\x1b[3J\x1b[3Jx\x1b[3J"},
			want:   "x",
		},
		{
			// A 4 KiB read can end mid-sequence; the tail must be held back
			// and matched against the start of the next chunk.
			name:   "split across chunks",
			chunks: []string{"a\x1b[", "3Jb"},
			want:   "ab",
		},
		{
			name:   "split after escape only",
			chunks: []string{"a\x1b", "[3Jb"},
			want:   "ab",
		},
		{
			name:   "split one byte at a time",
			chunks: []string{"a", "\x1b", "[", "3", "J", "b"},
			want:   "ab",
		},
		{
			// A held-back prefix that turns out NOT to be ED 3 must still be
			// delivered, in order, with whatever followed it.
			name:   "false prefix is released",
			chunks: []string{"a\x1b[", "2Jb"},
			want:   "a\x1b[2Jb",
		},
		{
			name:   "erase in line is untouched",
			chunks: []string{"a\x1b[Kb\x1b[0Jc"},
			want:   "a\x1b[Kb\x1b[0Jc",
		},
		{
			// The pseudo console's start-up negotiation: DA1 passes (the
			// terminal should answer it), the two mode requests do not.
			name:   "console start-up mode requests",
			chunks: []string{"\x1b[c\x1b[?1004h\x1b[?9001h"},
			want:   "\x1b[c",
		},
		{
			// Disabling is always allowed through — if an app did manage to
			// turn one on, its reset must reach the terminal.
			name:   "mode resets pass through",
			chunks: []string{"\x1b[?9001l\x1b[?1004l"},
			want:   "\x1b[?9001l\x1b[?1004l",
		},
		{
			name:   "win32 input mode split across reads",
			chunks: []string{"a\x1b[?90", "01hb"},
			want:   "ab",
		},
		{
			// A held-back tail that could still become the LONGEST sequence
			// must not be released just because a shorter one ruled it out.
			name:   "ambiguous prefix resolves to the longer sequence",
			chunks: []string{"x\x1b[?1004", "hy"},
			want:   "xy",
		},
		{
			name:   "two filtered sequences back to back",
			chunks: []string{"\x1b[3J\x1b[?9001hkeep"},
			want:   "keep",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m hostMirror
			if got := forceStrip(&m, tc.chunks...); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestHostMirrorForwardIsPlatformGated documents the wiring: the filter only
// engages where conhost manufactures the clear.
func TestHostMirrorForwardIsPlatformGated(t *testing.T) {
	var m hostMirror
	in := []byte("x\x1b[3Jy")
	got := string(m.forward(in))
	want := "x\x1b[3Jy"
	if filterHostMirror {
		want = "xy"
	}
	if got != want {
		t.Errorf("forward = %q, want %q (filterHostMirror=%v)", got, want, filterHostMirror)
	}
}
