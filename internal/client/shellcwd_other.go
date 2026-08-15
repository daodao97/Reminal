// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build !windows

package client

// shellCwdWindows reads a process's working directory out of its PEB — Win32
// only; the Unix paths live in shellCwd's switch directly.
func shellCwdWindows(pid int) string { return "" }
