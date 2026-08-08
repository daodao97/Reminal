// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build darwin

package session

import (
	"time"

	"golang.org/x/sys/unix"
)

// procStartTime returns when the process now at pid was started, per the kernel.
// Used to detect PID reuse: if this is meaningfully later than the session's
// recorded StartedAt, the OS handed the PID to a different, newer process and
// our session is actually dead. Best-effort — (zero, false) on any sysctl error,
// which the caller treats as "can't tell, assume not reused" so a live session
// is never pruned on missing evidence.
func procStartTime(pid int) (time.Time, bool) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil {
		return time.Time{}, false
	}
	tv := kp.Proc.P_starttime
	return time.Unix(int64(tv.Sec), int64(tv.Usec)*1000), true
}
