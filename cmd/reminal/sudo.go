// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package main

import (
	"errors"
	"os"
	"os/exec"
)

// needsSudoRetry reports whether err is a permission failure writing the
// root-owned owner store (/etc/reminal) while we're not root — i.e. the exact
// same command would succeed re-run under sudo. Owner mutations are atomic, so
// nothing was applied when the write was denied and re-running is safe.
func needsSudoRetry(err error) bool {
	return err != nil && errors.Is(err, os.ErrPermission) && os.Geteuid() != 0
}

// sudoReexec re-runs this exact reminal invocation under sudo, wiring the
// terminal through so sudo can prompt for a password interactively. The child
// does the work and prints its own output; we return its exit status, so the
// caller should return immediately after.
func sudoReexec() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command("sudo", append([]string{exe}, os.Args[1:]...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
