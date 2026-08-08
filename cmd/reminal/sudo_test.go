// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"
)

func TestNeedsSudoRetry(t *testing.T) {
	// A permission error writing the owner store, wrapped like the client does.
	permErr := fmt.Errorf("modifying owners writes /etc/reminal — re-run with sudo: %w",
		&os.PathError{Op: "open", Path: "/etc/reminal/x.tmp", Err: syscall.EACCES})

	// Only non-root benefits from re-running under sudo; as root the write would
	// have succeeded, so there'd be no permission error to retry in the first place.
	if os.Geteuid() != 0 && !needsSudoRetry(permErr) {
		t.Fatal("a permission error while non-root should trigger a sudo retry")
	}
	if os.Geteuid() == 0 && needsSudoRetry(permErr) {
		t.Fatal("root should never need a sudo retry")
	}

	if needsSudoRetry(errors.New("some other failure")) {
		t.Fatal("a non-permission error must not trigger a sudo retry")
	}
	if needsSudoRetry(nil) {
		t.Fatal("nil must not trigger a sudo retry")
	}
}
