// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build windows

package client

// On Windows the clears and input-mode requests in the PTY stream are mostly not
// the user's doing but the pseudo console's own — see hostMirror.
const filterHostMirror = true
