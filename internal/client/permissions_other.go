// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build !darwin

package client

import "fmt"

// EnsurePermissions is a no-op off macOS — Screen Recording / Accessibility /
// Automation (TCC) are macOS concepts. Linux window mirroring has no per-app gate.
func EnsurePermissions() error {
	fmt.Println("These permissions are macOS-only; nothing to configure here.")
	return nil
}

// RequestPermission is a no-op off macOS.
func RequestPermission(which string) error { return nil }
