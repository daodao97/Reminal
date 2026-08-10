// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build !darwin

package client

import "fmt"

// EnsureScreenRecording is a no-op off macOS — Screen Recording (TCC) is a macOS
// concept. Linux window mirroring has no per-app screen-capture gate.
func EnsureScreenRecording() error {
	fmt.Println("Screen Recording permission is macOS-only; nothing to configure here.")
	return nil
}

// RequestScreenRecordingViaHelper is a no-op off macOS.
func RequestScreenRecordingViaHelper() error { return nil }
