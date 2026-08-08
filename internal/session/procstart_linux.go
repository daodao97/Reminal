// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build linux

package session

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// procStartTime returns when the process now at pid was started, derived from
// /proc/<pid>/stat field 22 (starttime, in clock ticks since boot) plus the
// system boot time from /proc/stat's "btime". Used to detect PID reuse — see the
// darwin twin for the contract. Best-effort: (zero, false) on any parse error.
//
// USER_HZ is assumed to be 100 (near-universal on Linux). An off value only
// scales the computed uptime; because reuse is detected with a wide tolerance
// and a recycled PID's process is always far newer than the stale record, the
// approximation can't misclassify a live session as reused.
func procStartTime(pid int) (time.Time, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return time.Time{}, false
	}
	s := string(b)
	// comm (field 2) is parenthesized and may contain spaces or ')', so scan
	// past the LAST ')': the fields after it begin at state (field 3).
	i := strings.LastIndexByte(s, ')')
	if i < 0 || i+2 >= len(s) {
		return time.Time{}, false
	}
	fields := strings.Fields(s[i+2:])
	const starttimeIdx = 19 // field 22 minus the 3 fields consumed before state
	if len(fields) <= starttimeIdx {
		return time.Time{}, false
	}
	ticks, err := strconv.ParseInt(fields[starttimeIdx], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	btime, ok := bootTimeUnix()
	if !ok {
		return time.Time{}, false
	}
	const userHZ = 100
	return time.Unix(btime+ticks/userHZ, 0), true
}

func bootTimeUnix() (int64, bool) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "btime "); ok {
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				return 0, false
			}
			return n, true
		}
	}
	return 0, false
}
