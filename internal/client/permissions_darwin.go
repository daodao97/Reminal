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

// RequestScreenRecordingViaHelper runs the capture helper's `request` mode to
// surface the Screen Recording (TCC) prompt, then returns. It's invoked by the
// hidden `__request-screen-recording` subcommand, which runs INSIDE the
// LaunchServices-launched .app — so the prompt is attributed to the bundle
// identity (sh.reminal), the single grant that also covers the background daemon.
func RequestScreenRecordingViaHelper() error {
	helper, err := captureHelperPath()
	if err != nil {
		return err
	}
	// Blocks until the user responds or the helper's timeout — its stdout
	// ("granted"/"denied") is informational; the visible prompt is the point.
	_ = exec.Command(helper, "request").Run()
	return nil
}

// EnsureScreenRecording implements `reminal permissions`. When reminal is
// installed as reminal.app, a normal foreground `reminal` in a terminal rides the
// TERMINAL's Screen Recording grant and never prompts for reminal's own identity,
// and the background daemon can't prompt at all — so window mirroring silently
// fails for daemon-spawned ("+") sessions. This re-launches the .app via
// LaunchServices with a hidden flag so the prompt is attributed to sh.reminal;
// granting it once covers every session, including the daemon's.
func EnsureScreenRecording() error {
	app := bundlePath()
	if app == "" {
		fmt.Println("Screen Recording permission only applies to the packaged reminal.app.")
		fmt.Println("This build isn't a bundle, so window mirroring uses your terminal's grant.")
		return nil
	}
	if err := exec.Command("/usr/bin/open", "-a", app, "--args", "__request-screen-recording").Run(); err != nil {
		return fmt.Errorf("launch reminal.app: %w", err)
	}
	fmt.Println("A “reminal would like to record the screen” dialog should appear.")
	fmt.Println("→ Click Open System Settings, then enable reminal under")
	fmt.Println("  Privacy & Security ▸ Screen & System Audio Recording.")
	fmt.Println()
	fmt.Println("That single grant lets background sessions (the “+” button) mirror windows.")
	return nil
}
