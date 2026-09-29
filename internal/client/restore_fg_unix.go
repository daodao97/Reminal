// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build !windows

package client

import "reminal/internal/pty"

// restoreForeground is the program in the foreground of term's shell and its
// command line, and whether the shell's own prompt is — nothing running in
// it. The terminal says which process group has the foreground.
func restoreForeground(term *pty.Session) (prog string, args []string, pid int, atPrompt bool) {
	pg := term.ForegroundPgrp()
	if pg <= 0 {
		return "", nil, 0, false
	}
	if pg == term.Pid() {
		return "", nil, 0, true
	}
	return foregroundProgram(pg, attentionForegroundName(pg)), processArgs(pg), pg, false
}
