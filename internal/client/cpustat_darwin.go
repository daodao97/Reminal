// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build darwin

package client

import (
	"os/exec"
	"regexp"
	"strconv"
)

// idleRe pulls the idle percentage out of top's summary line:
//
//	CPU usage: 11.44% user, 7.39% sys, 81.17% idle
var idleRe = regexp.MustCompile(`([0-9.]+)% idle`)

// cpuPercent returns real CPU utilization (0..100) on macOS. There's no sysctl
// for aggregate CPU ticks (kern.cp_time doesn't exist here) and the true source
// — mach host_statistics — needs cgo, which reminal's static CGO_ENABLED=0 build
// can't use. So sample `top` instead: `top -l1 -n0` prints one current reading
// (verified to track live load, not a since-boot average) in ~200ms and skips
// the process list. Utilization = 100 − idle. Best-effort: any failure → unknown.
func cpuPercent() (float64, bool) {
	out, err := exec.Command("/usr/bin/top", "-l1", "-n0").Output()
	if err != nil {
		return 0, false
	}
	m := idleRe.FindSubmatch(out)
	if m == nil {
		return 0, false
	}
	idle, err := strconv.ParseFloat(string(m[1]), 64)
	if err != nil {
		return 0, false
	}
	busy := 100 - idle
	if busy < 0 {
		busy = 0
	}
	if busy > 100 {
		busy = 100
	}
	return busy, true
}
