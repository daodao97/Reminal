// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build !windows

package client

// A Unix pty relays its shell and nothing else: an erase-scrollback or an input
// mode on the stream is something the app genuinely asked for, so it reaches the
// host terminal untouched — see hostMirror.
const filterHostMirror = false
