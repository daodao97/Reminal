// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build !darwin && !windows

package client

// processArgsNative: Linux reads /proc directly (see processArgs); others
// have only ps.
func processArgsNative(int) ([]string, bool) { return nil, false }
