// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package config

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

const (
	DefaultPort = "8080"

	DefaultLocalRelay = "ws://localhost:8080/ws"
	DefaultLocalWeb   = "http://localhost:8080"
)

// shellCandidates is consulted in order when $SHELL is unset. Tries the
// common interactive shells in roughly Mac→Linux order; /bin/sh is the
// last-resort POSIX fallback that exists on every Unix.
var shellCandidates = []string{"/bin/zsh", "/bin/bash", "/bin/sh"}

// Upstream defaults keep a plain source build compatible with the public
// service. Forks and release builders can replace either value with -ldflags;
// runtime REMINAL_RELAY / REMINAL_WEB and REMINAL_LOCAL take precedence.
var (
	DefaultCloudRelay = "wss://reminal-relay.futuristic.workers.dev/ws"
	DefaultCloudWeb   = "https://reminal-relay.futuristic.workers.dev"
)

func RelayWS() string {
	if v := os.Getenv("REMINAL_RELAY"); v != "" {
		return strings.TrimRight(v, "/")
	}
	if os.Getenv("REMINAL_LOCAL") == "1" {
		return DefaultLocalRelay
	}
	// A single runtime URL is enough: keep links and sockets on the same host
	// instead of falling back to a differently compiled-in service.
	if v := os.Getenv("REMINAL_WEB"); v != "" {
		return relayFromWeb(v)
	}
	if DefaultCloudRelay != "" {
		return strings.TrimRight(DefaultCloudRelay, "/")
	}
	return relayFromWeb(DefaultCloudWeb)
}

func WebURL() string {
	if v := os.Getenv("REMINAL_WEB"); v != "" {
		return strings.TrimRight(v, "/")
	}
	if os.Getenv("REMINAL_LOCAL") == "1" {
		return DefaultLocalWeb
	}
	if v := os.Getenv("REMINAL_RELAY"); v != "" {
		return webFromRelay(v)
	}
	if DefaultCloudWeb != "" {
		return strings.TrimRight(DefaultCloudWeb, "/")
	}
	return webFromRelay(DefaultCloudRelay)
}

func relayFromWeb(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	case "wss", "ws":
	default:
		return ""
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/ws"
	u.RawQuery, u.Fragment = "", ""
	return u.String()
}

func webFromRelay(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	switch u.Scheme {
	case "wss":
		u.Scheme = "https"
	case "ws":
		u.Scheme = "http"
	case "https", "http":
	default:
		return ""
	}
	u.Path = strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/ws")
	u.RawQuery, u.Fragment = "", ""
	return strings.TrimRight(u.String(), "/")
}

func SessionWS(sessionID, role string) string {
	base := RelayWS()
	sessionID = strings.ToUpper(strings.TrimSpace(sessionID))
	return fmt.Sprintf("%s/%s/%s", base, sessionID, role)
}

// RendezvousWS builds the WebSocket URL for a `reminal copy`/`paste`
// rendezvous. RelayWS() ends in "/ws" (the shell-session prefix); the
// rendezvous lives under "/rv" on the same host, so we swap the suffix.
// role is "source" or "paste"; code is the canonical (uppercase,
// dash-free) transfer code that keys the relay's RendezvousRoom.
func RendezvousWS(code, role string) string {
	base := strings.TrimSuffix(RelayWS(), "/ws")
	return fmt.Sprintf("%s/rv/%s/%s", base, strings.ToUpper(code), role)
}

func Shell() string {
	if runtime.GOOS == "windows" {
		return windowsShell()
	}
	if v := os.Getenv("SHELL"); v != "" {
		return v
	}
	// $SHELL unset (rare on interactive terminals, common in cron / systemd
	// service contexts). Probe the candidate list and return the first that
	// exists; falling back to /bin/sh which is POSIX-required.
	for _, candidate := range shellCandidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return "/bin/sh"
}

// windowsShell picks the session shell on Windows. $SHELL is respected when
// set (users of git-bash / MSYS set it deliberately); otherwise prefer
// PowerShell 7 (pwsh), then Windows PowerShell, then %COMSPEC%/cmd — the same
// order Windows Terminal effectively presents.
func windowsShell() string {
	if v := os.Getenv("SHELL"); v != "" {
		return v
	}
	for _, candidate := range []string{"pwsh.exe", "powershell.exe"} {
		if p, err := exec.LookPath(candidate); err == nil {
			return p
		}
	}
	if v := os.Getenv("COMSPEC"); v != "" {
		return v
	}
	return "cmd.exe"
}

// DefaultSnapshotScrollbackLines is how many lines of scrollback history a
// fresh-attach snapshot includes by default. Generous enough to scroll back
// through a long session, bounded so the snapshot (and the agent's emulator
// memory) stay reasonable. Override with REMINAL_SCROLLBACK_LINES.
const DefaultSnapshotScrollbackLines = 10000

// SnapshotScrollbackLines returns how many scrollback lines to include in the
// attach snapshot. REMINAL_SCROLLBACK_LINES overrides the default; 0 means
// "screen only, no history"; negative or unparseable falls back to the default.
func SnapshotScrollbackLines() int {
	v := os.Getenv("REMINAL_SCROLLBACK_LINES")
	if v == "" {
		return DefaultSnapshotScrollbackLines
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return DefaultSnapshotScrollbackLines
	}
	return n
}

// DefaultSnapshotScrollbackBytes caps the rendered size of the scrollback
// portion of an attach snapshot, so a wide/colorful history can't balloon the
// payload even within the line limit. The visible screen is always included on
// top of this.
const DefaultSnapshotScrollbackBytes = 2 << 20 // 2 MiB

// SnapshotScrollbackBytes returns the byte cap for snapshot scrollback.
// REMINAL_SCROLLBACK_BYTES overrides the default; 0 means "no byte cap" (the
// line cap still applies); negative or unparseable falls back to the default.
func SnapshotScrollbackBytes() int {
	v := os.Getenv("REMINAL_SCROLLBACK_BYTES")
	if v == "" {
		return DefaultSnapshotScrollbackBytes
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return DefaultSnapshotScrollbackBytes
	}
	return n
}
