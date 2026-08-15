// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build windows

package client

// Live working directory of another process on Windows: there is no /proc and
// no lsof, but every process records its current directory in its PEB
// (RTL_USER_PROCESS_PARAMETERS.CurrentDirectory), readable cross-process with
// PROCESS_VM_READ on same-user targets. This is the same mechanism Process
// Explorer uses.
//
// One important wrinkle: PowerShell deliberately does NOT chdir its process
// when you `cd` (Set-Location is per-runspace), so its PEB value is frozen at
// launch. Two mitigations layered here:
//   - We prefer the most recently started DESCENDANT of the shell — an
//     editor, build, or git launched from the shell carries the real
//     directory in its own PEB — loosely mirroring the Unix foreground-
//     process-group behavior.
//   - The installer's profile block installs a LocationChangedAction hook
//     that syncs [Environment]::CurrentDirectory on every cd, making the
//     shell's own PEB truthful too (see install.ps1).
// cmd.exe needs neither: it chdirs natively.

import (
	"strings"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 64-bit PEB/parameter offsets (identical on amd64 and arm64; we never read
// 32-bit WOW64 targets — pebCwd filters them out).
const (
	pebProcessParametersOff = 0x20 // PEB.ProcessParameters
	paramsCurrentDirOff     = 0x38 // RTL_USER_PROCESS_PARAMETERS.CurrentDirectory.DosPath
)

func shellCwdWindows(pid int) string {
	// Prefer the youngest descendant (the "foreground-ish" process); fall
	// back to the shell itself. Any candidate that yields no path falls
	// through to the next.
	for _, candidate := range cwdCandidates(pid) {
		if p := pebCwd(candidate); p != "" {
			return p
		}
	}
	return ""
}

// cwdCandidates returns pids to try, youngest-started descendant first,
// the shell itself last.
func cwdCandidates(shellPid int) []uint32 {
	self := []uint32{uint32(shellPid)}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return self
	}
	defer windows.CloseHandle(snap)

	children := map[uint32][]uint32{}
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err := windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		children[pe.ParentProcessID] = append(children[pe.ParentProcessID], pe.ProcessID)
	}

	// Collect the descendant set breadth-first (bounded — a runaway tree is
	// not worth more than a screenful of lookups).
	var descendants []uint32
	queue := []uint32{uint32(shellPid)}
	for len(queue) > 0 && len(descendants) < 64 {
		next := queue[0]
		queue = queue[1:]
		for _, c := range children[next] {
			descendants = append(descendants, c)
			queue = append(queue, c)
		}
	}
	if len(descendants) == 0 {
		return self
	}

	// Youngest creation time first.
	type cand struct {
		pid   uint32
		start uint64
	}
	cands := make([]cand, 0, len(descendants))
	for _, pid := range descendants {
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
		if err != nil {
			continue
		}
		var creation, exit, kernel, user windows.Filetime
		err = windows.GetProcessTimes(h, &creation, &exit, &kernel, &user)
		windows.CloseHandle(h)
		if err != nil {
			continue
		}
		cands = append(cands, cand{pid, uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime)})
	}
	// Insertion sort by start desc — n is tiny.
	for i := 1; i < len(cands); i++ {
		for j := i; j > 0 && cands[j].start > cands[j-1].start; j-- {
			cands[j], cands[j-1] = cands[j-1], cands[j]
		}
	}
	out := make([]uint32, 0, len(cands)+1)
	for _, c := range cands {
		out = append(out, c.pid)
	}
	return append(out, uint32(shellPid))
}

// pebCwd reads pid's current directory out of its PEB, or "".
func pebCwd(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)

	// Skip 32-bit (WOW64) targets: their PEB uses different offsets, and a
	// wrong-layout read would return garbage rather than failing.
	var wow64 bool
	if err := windows.IsWow64Process(h, &wow64); err != nil || wow64 {
		return ""
	}

	var pbi windows.PROCESS_BASIC_INFORMATION
	var retLen uint32
	if err := windows.NtQueryInformationProcess(h, windows.ProcessBasicInformation,
		unsafe.Pointer(&pbi), uint32(unsafe.Sizeof(pbi)), &retLen); err != nil {
		return ""
	}

	readPtr := func(addr uintptr) (uintptr, bool) {
		var v uintptr
		var n uintptr
		err := windows.ReadProcessMemory(h, addr, (*byte)(unsafe.Pointer(&v)), unsafe.Sizeof(v), &n)
		return v, err == nil && n == unsafe.Sizeof(v)
	}

	params, ok := readPtr(uintptr(unsafe.Pointer(pbi.PebBaseAddress)) + pebProcessParametersOff)
	if !ok || params == 0 {
		return ""
	}
	// UNICODE_STRING: Length u16, MaximumLength u16, (pad), Buffer ptr at +8.
	var lengths uint32
	var n uintptr
	if err := windows.ReadProcessMemory(h, params+paramsCurrentDirOff,
		(*byte)(unsafe.Pointer(&lengths)), 4, &n); err != nil || n != 4 {
		return ""
	}
	strLen := uintptr(lengths & 0xFFFF) // bytes, not chars
	if strLen == 0 || strLen > 32*1024 {
		return ""
	}
	buf, ok := readPtr(params + paramsCurrentDirOff + 8)
	if !ok || buf == 0 {
		return ""
	}
	raw := make([]uint16, strLen/2)
	if err := windows.ReadProcessMemory(h, buf,
		(*byte)(unsafe.Pointer(&raw[0])), strLen, &n); err != nil || n != strLen {
		return ""
	}
	path := string(utf16.Decode(raw))
	// The PEB stores the directory with a trailing backslash; drop it except
	// for bare drive roots ("C:\"), which need it to stay meaningful.
	if len(path) > 3 {
		path = strings.TrimSuffix(path, `\`)
	}
	return path
}
