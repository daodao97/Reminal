// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Healing a stuck replayd (macOS).
//
// replayd is macOS's per-user screen-capture service. It can get into a state
// where, for every capture request, it hands the permission service (tccd) a
// reference to the wrong process. tccd cannot resolve it ("Failed to build
// 'accessingProcess' from target_token"), answers with nothing, and
// ScreenCaptureKit turns that into "The user declined TCCs for application,
// window, display capture" — although reminal is allowed to record the screen.
// Every window and desktop view then fails. Toggling the permission or
// restarting reminal does not help, because the fault is inside replayd;
// restarting replayd does, and launchd starts a fresh one at once. Seen on
// macOS 14.8 on 2026-10-03, after replayd had been up for days.
//
// So when a capture fails with exactly that error, and reminal's own preflight
// says screen recording IS granted, the daemon restarts replayd once, then
// leaves it alone for a while. The session's normal retry then succeeds. A
// genuine "no" (preflight says not granted) is the person's choice and is left
// alone: restarting replayd would not change it.

const (
	// The ScreenCaptureKit error this heals. Matched loosely: it reaches us
	// inside the helper's "capture <id> ended: …" line.
	replaydDeclinedMarker = "declined TCCs"
	// At most one restart per this long, so a fault replayd's restart does not
	// fix can never become a loop that keeps killing it.
	replaydHealCooldown = 10 * time.Minute
)

// Seams for tests; real implementations below.
var (
	replaydHealGOOS       = runtime.GOOS
	replaydCaptureGranted = captureGrantedByPreflight
	replaydPIDs           = userReplaydPIDs
	replaydKill           = killPID
	replaydLog            = func(format string, a ...any) { fmt.Fprintf(os.Stderr, format, a...) }
	replaydNow            = time.Now
	replaydHeal           struct {
		sync.Mutex
		last time.Time
	}
)

// noteCaptureFailure looks at one line the capture helper wrote to stderr and,
// if it is the stuck-replayd symptom, starts a heal in the background.
func noteCaptureFailure(line string) {
	if replaydHealGOOS != "darwin" || !strings.Contains(line, replaydDeclinedMarker) {
		return
	}
	go healStuckReplayd()
}

// healStuckReplayd restarts replayd if capture is really granted and no heal
// ran within the cooldown. Returns whether it restarted anything (for tests).
func healStuckReplayd() bool {
	replaydHeal.Lock()
	now := replaydNow()
	if !replaydHeal.last.IsZero() && now.Sub(replaydHeal.last) < replaydHealCooldown {
		replaydHeal.Unlock()
		return false
	}
	replaydHeal.last = now
	replaydHeal.Unlock()

	if !replaydCaptureGranted() {
		replaydLog("reminal: screen capture was refused and Screen Recording is not granted to reminal — run `reminal permissions`\n")
		return false
	}
	pids := replaydPIDs()
	if len(pids) == 0 {
		return false
	}
	killed := 0
	for _, pid := range pids {
		if replaydKill(pid) == nil {
			killed++
		}
	}
	if killed == 0 {
		return false
	}
	replaydLog("reminal: macOS's screen-capture service (replayd) refused a capture reminal is allowed to make; restarted it so capture can resume\n")
	return true
}

// captureGrantedByPreflight asks the capture helper's non-prompting preflight,
// run in the daemon's own (granted) context, whether screen recording is
// allowed. The same check `reminal permissions` relies on.
func captureGrantedByPreflight() bool {
	p, err := captureHelperPath()
	if err != nil {
		return false
	}
	out, err := run(p, "check")
	return err == nil && strings.TrimSpace(out) == "ok"
}

// userReplaydPIDs lists replayd processes belonging to this user. Only those
// can be signalled without administrator rights, and only those serve us.
func userReplaydPIDs() []int {
	out, err := run("pgrep", "-x", "-U", strconv.Itoa(os.Getuid()), "replayd")
	if err != nil {
		return nil
	}
	return parsePIDs(out)
}

func parsePIDs(out string) []int {
	var pids []int
	for _, f := range strings.Fields(out) {
		if n, err := strconv.Atoi(f); err == nil && n > 1 {
			pids = append(pids, n)
		}
	}
	return pids
}

// killPID stops a process outright. replayd ignores a plain termination
// request and launchd refuses to kickstart it ("Operation not permitted"), but
// a forced stop works and launchd starts a fresh one immediately.
func killPID(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
