// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package pty

import (
	"os"
	"strings"
	"testing"
)

// TestShellEnvDisablesAppleSessions guards the fix for the macOS "…/.zsh_sessions
// /….session: command not found: Saving" error: the inner shell must have
// Terminal's session save/restore disabled and must not inherit its session
// file pointer, while unrelated env is preserved.
func TestShellEnvDisablesAppleSessions(t *testing.T) {
	t.Setenv("SHELL_SESSION_FILE", "/tmp/whatever.session")
	env := shellEnv([]string{"FOO=bar"})

	var hasDisable, hasFile, hasFoo bool
	for _, kv := range env {
		switch {
		case kv == "SHELL_SESSIONS_DISABLE=1":
			hasDisable = true
		case strings.HasPrefix(kv, "SHELL_SESSION_FILE="):
			hasFile = true
		case kv == "FOO=bar":
			hasFoo = true
		}
	}
	if !hasDisable {
		t.Fatal("SHELL_SESSIONS_DISABLE=1 must be set for the inner shell")
	}
	if hasFile {
		t.Fatal("inherited SHELL_SESSION_FILE must be dropped")
	}
	if !hasFoo {
		t.Fatal("caller-provided env must be preserved")
	}
	_ = os.Environ
}
