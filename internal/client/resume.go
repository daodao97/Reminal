// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"time"

	"github.com/reminal/reminal/internal/pty"
)

// ResumeState is what the new process reconstructs from env vars after
// an Exec restart. nil from LoadResumeState() means "not resuming —
// take the normal fresh-startup path." Hot restart is Unix-only (it rides
// syscall.Exec); the type lives here untagged because the fresh-startup
// callers reference it on every platform.
type ResumeState struct {
	SessionID string
	PIN       string
	PinHash   string
	Token     string
	StartedAt time.Time
	PTY       *pty.Session
	// HandshakeAddr is Windows-only: the OLD agent's loopback listener, so the
	// resumed agent can report "registered" and release it to exit. Unix's
	// exec-based restart has no separate process to notify.
	HandshakeAddr string
}
