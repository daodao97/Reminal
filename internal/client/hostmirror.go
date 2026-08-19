// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import "bytes"

// eraseScrollback is ED 3 — "erase the saved lines", i.e. throw away the
// terminal's scrollback. Everything above the visible screen, gone.
var eraseScrollback = []byte("\x1b[3J")

// hostMirror filters the PTY stream on its way to the HOST terminal only (the
// bytes viewers and the snapshot emulator see are never touched — they must
// reflect exactly what the app did).
//
// It exists for one sequence, and only on Windows. There, the shell does not
// have to ask for a clear for the host to get one: PowerShell blanks its whole
// buffer through the Win32 fill API on a resize, and conhost's PowerShell shim
// turns that into "ESC[H ESC[2J ESC[3J" on the PTY stream (microsoft/terminal,
// src/host/_stream.cpp: WriteClearScreen). Mirrored verbatim, the ED 3 deletes
// the scrollback of the terminal the user ran `reminal` in — the join banner,
// the QR code, and everything they had on screen before starting. A viewer
// connecting or a phone rotating is enough to trigger it.
//
// So the host mirror drops ED 3. ED 2 still passes, so the shell's repaint
// still lands on a properly cleared screen and the host keeps rendering
// identically to the viewers; only the history ABOVE the screen is spared.
// The trade: a deliberate `Clear-Host` clears the host terminal's screen but
// leaves its scrollback (viewers still get the full clear). That is the right
// way round — reminal must never be the reason a terminal's history vanishes.
type hostMirror struct {
	// pending holds a trailing partial ED 3 (up to "\x1b[3") so a sequence
	// split across two reads is still recognised when the rest arrives.
	pending []byte
}

// forward returns the bytes to write to the host terminal for this chunk.
// The input slice is never modified — the caller still records it verbatim.
func (m *hostMirror) forward(p []byte) []byte {
	if !stripEraseScrollback {
		return p
	}
	return m.stripSequence(p)
}

// stripSequence removes every eraseScrollback from p, carrying a split sequence
// across chunk boundaries. Split out from forward so the platform gate and the
// stream logic can be tested independently, on any OS.
func (m *hostMirror) stripSequence(p []byte) []byte {
	if len(m.pending) > 0 {
		joined := make([]byte, 0, len(m.pending)+len(p))
		joined = append(joined, m.pending...)
		joined = append(joined, p...)
		m.pending = m.pending[:0]
		p = joined
	}
	var out []byte
	stripped := false
	for {
		i := bytes.Index(p, eraseScrollback)
		if i < 0 {
			break
		}
		out = append(out, p[:i]...)
		p = p[i+len(eraseScrollback):]
		stripped = true
	}
	if n := trailingPrefixLen(p, eraseScrollback); n > 0 {
		m.pending = append(m.pending, p[len(p)-n:]...)
		p = p[:len(p)-n]
	}
	if !stripped {
		return p
	}
	return append(out, p...)
}

// trailingPrefixLen returns the length of the longest proper prefix of seq that
// p ends with (0 if none) — the bytes that must be held back because the next
// chunk might complete the sequence.
func trailingPrefixLen(p, seq []byte) int {
	n := len(seq) - 1
	if n > len(p) {
		n = len(p)
	}
	for ; n > 0; n-- {
		if bytes.HasSuffix(p, seq[:n]) {
			return n
		}
	}
	return 0
}
