// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build windows

package client

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"

	"reminal/internal/pty"
)

// restoreForeground on Windows: a console has no foreground process group
// to ask, so the program is found among the shell's own children — the
// newest one that is a coding agent reminal can resume (claude.exe, or node
// running codex's or gemini's script). No children at all is the prompt.
func restoreForeground(term *pty.Session) (prog string, args []string, atPrompt bool) {
	kids := childProcesses(uint32(term.Pid()))
	if len(kids) == 0 {
		return "", nil, true
	}
	if p, a := agentAmong(kids, 2); p != "" {
		return p, a, false
	}
	return "", nil, false
}

// shimHosts run a CLI's shim (npm's claude.cmd, codex.ps1) and are not the
// program themselves: the agent is their child.
var shimHosts = map[string]bool{"cmd": true, "powershell": true, "pwsh": true, "conhost": true}

// agentAmong is the newest resumable agent among procs, looking through shim
// hosts down to depth levels.
func agentAmong(procs []childProc, depth int) (string, []string) {
	for _, k := range procs {
		comm := strings.TrimSuffix(strings.ToLower(k.exe), ".exe")
		if shimHosts[comm] {
			if depth > 0 {
				if p, a := agentAmong(childProcesses(k.pid), depth-1); p != "" {
					return p, a
				}
			}
			continue
		}
		a := processArgs(int(k.pid))
		p := comm
		if !isAgentProgram(p) {
			p = programFromArgs(a, comm)
		}
		if _, ok := resumers[p]; ok {
			return p, a
		}
	}
	return "", nil
}

type childProc struct {
	pid   uint32
	exe   string
	start uint64
}

// childProcesses are pid's direct children, newest first.
func childProcesses(pid uint32) []childProc {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var out []childProc
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err := windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		if pe.ParentProcessID != pid || pe.ProcessID == pid {
			continue
		}
		c := childProc{pid: pe.ProcessID, exe: windows.UTF16ToString(pe.ExeFile[:])}
		if h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pe.ProcessID); err == nil {
			var creation, exit, kernel, user windows.Filetime
			if windows.GetProcessTimes(h, &creation, &exit, &kernel, &user) == nil {
				c.start = uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime)
			}
			windows.CloseHandle(h)
		}
		out = append(out, c)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].start > out[j-1].start; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
