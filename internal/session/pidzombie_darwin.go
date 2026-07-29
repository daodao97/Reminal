// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build darwin

package session

import "golang.org/x/sys/unix"

// szombDarwin is the BSD proc state for a zombie (defunct) process. x/sys/unix
// doesn't export it, so mirror the value from <sys/proc.h>: SIDL=1, SRUN=2,
// SSLEEP=3, SSTOP=4, SZOMB=5.
const szombDarwin = 5

// pidIsZombie reports whether pid is a zombie — a process that has exited but
// hasn't been reaped by its parent yet. Signal 0 still succeeds on such a PID
// (it occupies the process table), so pidAlive needs this to tell "running"
// from "defunct". Best-effort: any sysctl error is treated as "not a zombie".
func pidIsZombie(pid int) bool {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil {
		return false
	}
	return kp.Proc.P_stat == szombDarwin
}
