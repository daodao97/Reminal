// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build !windows

package client

// Unix ptys have no equivalent of conhost's PowerShell shim: an erase-scrollback
// on the stream is something the app genuinely emitted (`clear`), so it passes
// through to the host terminal untouched — see hostMirror.
const stripEraseScrollback = false
