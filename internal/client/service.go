// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
)

// Service identifiers + the pure generators for the two platforms' service
// definitions. Kept here (build-tag-free) so they can be unit-tested on any OS —
// a malformed plist or a dropped systemd directive (e.g. KillMode=process) would
// otherwise only surface at install time on the target platform.
const (
	daemonLabel = "sh.reminal.daemon"      // macOS LaunchAgent label
	daemonUnit  = "reminal-daemon.service" // Linux systemd --user unit
)

// launchdPlist is the macOS LaunchAgent that runs `reminal daemon` at login and
// keeps it alive. exe/logPath are XML-escaped for the plist string values.
//
// Deliberately NO `ProcessType` key: ProcessType=Background clamps the daemon to
// low-priority efficiency-core QoS, and sessions it spawns INHERIT that QoS — so
// interactive terminals and window streaming spawned via the "+" ran throttled
// (~5 fps, sluggish startup). Omitting it leaves the daemon at Standard QoS, so
// spawned sessions run at normal priority like a terminal-started one.
func launchdPlist(exe, logPath string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>daemon</string>
	</array>
	<key>RunAtLoad</key><true/>
	<key>KeepAlive</key><true/>
	<key>StandardOutPath</key><string>%s</string>
	<key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, daemonLabel, xmlEscape(exe), xmlEscape(logPath), xmlEscape(logPath))
}

// systemdUnit is the Linux systemd --user unit that runs `reminal daemon`.
// KillMode=process is load-bearing: without it, stopping/restarting the daemon
// would SIGKILL the "+"-spawned sessions in its cgroup (setsid escapes the
// process group, not the cgroup) — see service_linux.go.
func systemdUnit(exe string) string {
	return fmt.Sprintf(`[Unit]
Description=reminal background host (keeps this machine reachable to its owners)
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=%s daemon
Restart=always
RestartSec=3
KillMode=process

[Install]
WantedBy=default.target
`, exe)
}

// xmlEscape escapes the characters that would break a plist string value.
func xmlEscape(s string) string {
	repl := map[rune]string{'&': "&amp;", '<': "&lt;", '>': "&gt;", '"': "&quot;", '\'': "&apos;"}
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if e, ok := repl[r]; ok {
			out = append(out, []rune(e)...)
		} else {
			out = append(out, r)
		}
	}
	return string(out)
}

// The background-host login service (a macOS LaunchAgent / Linux systemd --user
// unit) runs `reminal daemon` at login and restarts it on crash, so an owned
// machine keeps a presence — see RunDaemon. It's installed when the machine gains
// an owner and removed when it loses its last one, so the service exists exactly
// when it's needed. Users don't manage it directly.
//
// The subtlety these functions exist to handle: enrolling an owner writes the
// root-owned owner store, so `reminal add owner` runs as root (via sudo). But the
// daemon must run as the HUMAN user — it needs ~/.reminal's keys and spawns the
// user's sessions — and a per-user service lives in the user's home and login
// domain, never root's. So we resolve the *target* user (SUDO_USER when we're
// root) and the platform code creates + loads the service into that user's
// domain (launchctl asuser / systemctl --user), chowning files to them.

// InstallDaemonService installs and starts the background-host login service for
// the owning user. Idempotent. Safe to call right after enrolling an owner, as
// root (under sudo) or as the user.
func InstallDaemonService() error {
	u, err := targetUser()
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// Follow symlinks so the service runs the REAL binary. On macOS the installed
	// CLI is a symlink into reminal.app; the daemon must exec the bundle's binary
	// directly so it runs as the sh.reminal identity — the one whose Screen
	// Recording grant covers the ("+") sessions the daemon spawns.
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	return installService(exe, u)
}

// UninstallDaemonService stops and removes the login service. Idempotent — a
// no-op (nil) when nothing is installed.
func UninstallDaemonService() error {
	u, err := targetUser()
	if err != nil {
		return err
	}
	return uninstallService(u)
}

// RestartDaemonService restarts the running background host so it re-execs a
// freshly-installed binary (e.g. after `reminal upgrade`). A no-op (nil) when the
// service isn't installed. Best-effort.
func RestartDaemonService() error {
	u, err := targetUser()
	if err != nil {
		return err
	}
	return restartService(u)
}

// EnsureDaemonInstalled is an idempotent CORRECTNESS check (deliberately NOT
// version-gated): whenever reminal is running from the macOS reminal.app bundle,
// the always-on daemon must exist to perform screen capture + input injection for
// every session under the one granted sh.reminal identity. If its login service is
// missing, install it. A no-op — cheap stat of the plist — when already present or
// when not running from a bundle (bare/dev builds, and Linux, use their existing
// paths). Safe to call on every startup and after an upgrade/migration, so a
// bundle-without-daemon self-heals regardless of which version introduced the gap.
func EnsureDaemonInstalled() {
	if !runningFromBundle() || DaemonServiceInstalled() {
		return
	}
	_ = InstallDaemonService()
}

// DaemonServiceInstalled reports whether the background-host login service is
// installed for the owning user. Lets callers decide whether to mention/refresh
// it (e.g. `reminal restart --all`). False on any lookup error.
func DaemonServiceInstalled() bool {
	u, err := targetUser()
	if err != nil {
		return false
	}
	return serviceInstalled(u)
}

// targetUser is the human user the service belongs to: SUDO_USER when we're root
// under sudo, otherwise the current user.
func targetUser() (*user.User, error) {
	if os.Geteuid() == 0 {
		if su := os.Getenv("SUDO_USER"); su != "" && su != "root" {
			return user.Lookup(su)
		}
		return nil, errors.New("run this as your user (or via sudo) so the background host is owned by you, not root")
	}
	return user.Current()
}
