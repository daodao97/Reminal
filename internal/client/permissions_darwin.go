// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build darwin

package client

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// bundlePath returns the path to the containing reminal.app if this binary lives
// inside one (…/reminal.app/Contents/MacOS/reminal), else "". Resolves symlinks
// first, since the installed CLI is a symlink into the bundle.
func bundlePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	macos := filepath.Dir(exe)      // …/Contents/MacOS
	contents := filepath.Dir(macos) // …/Contents
	app := filepath.Dir(contents)   // …/reminal.app
	if filepath.Base(macos) == "MacOS" && filepath.Base(contents) == "Contents" &&
		strings.HasSuffix(app, ".app") {
		return app
	}
	return ""
}

// RequestAllPermissions surfaces the three TCC prompts reminal needs — Screen
// Recording (mirror windows/desktop), Accessibility (injected mouse/scroll/click
// for remote control), and Automation (control "System Events" for typing +
// window focus) — one after another. Invoked by the hidden `__request-permissions`
// subcommand, which runs INSIDE the LaunchServices-launched .app so each prompt is
// attributed to the bundle identity (sh.reminal); those grants then cover the
// background daemon's ("+") sessions too.
func RequestAllPermissions() error {
	if helper, err := captureHelperPath(); err == nil {
		_ = exec.Command(helper, "request").Run() // Screen Recording — system-owned dialog
		time.Sleep(500 * time.Millisecond)        // let its dialog surface first
		// Accessibility's dialog is dropped if the requester exits while another TCC
		// dialog is up (why it used to need a second run) — `accessibility` now stays
		// alive polling until granted or ~30s, so this blocks here.
		_ = exec.Command(helper, "accessibility").Run()
	}
	// Automation: a benign query to System Events triggers the "…wants to control
	// System Events" prompt. Blocks until the user answers.
	_ = exec.Command("/usr/bin/osascript", "-e",
		`tell application "System Events" to get name of first process`).Run()
	return nil
}

// EnsurePermissions implements `reminal permissions`. When reminal is installed as
// reminal.app, a normal foreground `reminal` in a terminal rides the TERMINAL's
// grants and never prompts for reminal's own identity, and the background daemon
// can't prompt at all — so viewing AND control silently fail for daemon-spawned
// ("+") sessions. This re-launches the .app via LaunchServices with a hidden flag
// so the prompts are attributed to sh.reminal; granting them once covers every
// session, including the daemon's.
func EnsurePermissions() error {
	app := bundlePath()
	if app == "" {
		fmt.Println("These permissions only apply to the packaged reminal.app.")
		fmt.Println("This build isn't a bundle, so it uses your terminal's grants.")
		return nil
	}
	if err := exec.Command("/usr/bin/open", "-a", app, "--args", "__request-permissions").Run(); err != nil {
		return fmt.Errorf("launch reminal.app: %w", err)
	}
	fmt.Println("Three permission dialogs will appear — grant all of them so both")
	fmt.Println("viewing AND remote control work from background (“+”) sessions:")
	fmt.Println()
	fmt.Println("  1. Screen Recording  — mirror windows and the desktop")
	fmt.Println("  2. Accessibility     — move the cursor, click, scroll, drag")
	fmt.Println("  3. Automation        — type text + focus windows (control “System Events”)")
	fmt.Println()
	fmt.Println("For #1 and #2, click Open System Settings and enable reminal; for #3 click OK.")
	fmt.Println("Granting all three now avoids surprise prompts when you're away from the Mac.")
	return nil
}
