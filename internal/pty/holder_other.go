// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build !windows

package pty

import "errors"

// RunHolder is the Windows ConPTY holder (`reminal __ptyhold`); Unix keeps
// the PTY in-process (exec-based hot restart needs no holder).
func RunHolder(sockPath, shell string) error {
	return errors.New("__ptyhold is Windows-only")
}
