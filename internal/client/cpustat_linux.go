// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build linux

package client

import (
	"os"
	"strconv"
	"strings"
	"sync"
)

// cpuStat holds the previous /proc/stat aggregate sample so cpuPercent can
// report utilization as the busy-fraction of ticks elapsed since the last call.
var (
	cpuStatMu sync.Mutex
	prevIdle  uint64
	prevTotal uint64
	havePrev  bool
)

// cpuPercent returns real CPU utilization (0..100) on Linux by diffing the
// aggregate "cpu" line of /proc/stat between successive calls. No cgo, no
// subprocess. The first call has no prior sample to diff against, so it seeds
// the baseline and reports "unknown" — the next poll (~1.5s later) yields a
// real number over that interval.
func cpuPercent() (float64, bool) {
	idle, total, ok := readProcStat()
	if !ok {
		return 0, false
	}
	cpuStatMu.Lock()
	defer cpuStatMu.Unlock()
	if !havePrev {
		prevIdle, prevTotal, havePrev = idle, total, true
		return 0, false // no delta yet
	}
	dIdle := idle - prevIdle
	dTotal := total - prevTotal
	prevIdle, prevTotal = idle, total
	if dTotal == 0 {
		return 0, false
	}
	busy := float64(dTotal-dIdle) / float64(dTotal) * 100
	if busy < 0 {
		busy = 0
	}
	if busy > 100 {
		busy = 100
	}
	return busy, true
}

// readProcStat parses the aggregate "cpu" line of /proc/stat and returns the
// idle ticks (idle + iowait) and the total across all states.
//
//	cpu  user nice system idle iowait irq softirq steal guest guest_nice
func readProcStat() (idle, total uint64, ok bool) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)[1:] // drop the "cpu" label
		var sum, idleV uint64
		for i, f := range fields {
			v, err := strconv.ParseUint(f, 10, 64)
			if err != nil {
				continue
			}
			sum += v
			if i == 3 || i == 4 { // idle (3) + iowait (4)
				idleV += v
			}
		}
		return idleV, sum, true
	}
	return 0, 0, false
}
