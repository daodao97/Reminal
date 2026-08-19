// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build !windows

package client

// inputBlocker is a Windows concern: UIPI, which has no equivalent here. macOS
// gates injection behind an Accessibility grant the user is prompted for, and
// X11/Wayland refuse or allow it wholesale — neither fails silently per-window.
func inputBlocker(string) string { return "" }
