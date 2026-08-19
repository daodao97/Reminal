// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build windows

package client

// On Windows the erase-scrollback in the PTY stream is usually not the user's
// doing but conhost's PowerShell shim reacting to a resize — see hostMirror.
const stripEraseScrollback = true
