// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build !windows

package pty

// freshenEnv is a no-op off Windows: a login shell re-sources the profile chain
// itself, which is the same job done at the right layer (see session_unix.go).
func freshenEnv(env []string) []string { return env }
