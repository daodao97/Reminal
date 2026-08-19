// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build windows

package client

// Detecting the one thing remote input cannot do on Windows.
//
// UIPI (User Interface Privilege Isolation) forbids a process from injecting
// input into a window that runs at a HIGHER integrity level than itself. reminal
// runs at whatever level the shell that started it did — Medium, normally — so
// an elevated window is simply out of reach.
//
// The part that makes this worth detecting is how total it is: while an elevated
// window holds the FOREGROUND, SendInput from a lower-integrity process is
// discarded outright — not merely the events aimed at that window. Every tap
// stops landing anywhere, the cursor itself stops moving, and the mirror looks
// frozen rather than blocked. Reported, reasonably, as "the mirror is broken".
//
// Nothing here fixes that; it is a security boundary, and the only way through
// it is to run the agent elevated — an admin-level agent driveable from a
// browser, which is not a default anyone should ship. What we can do is say so.

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// selfElevated is whether OUR process is elevated, resolved once. The
// comparison that matters is relative: an elevated agent can drive an elevated
// window, and nothing here applies.
var selfElevated = func() bool {
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &tok); err != nil {
		return false
	}
	defer tok.Close()
	return tok.IsElevated()
}()

// processOutOfReach reports whether pid runs at a level our injected input
// cannot cross.
//
// The direct question is "is it elevated", answered from its token. A refusal to
// open the token IS the answer too: a Medium process is allowed
// PROCESS_QUERY_LIMITED_INFORMATION on anything, so being denied the token of a
// process we can otherwise see means it sits above us. (That asymmetry is what
// makes the check reliable without parsing integrity SIDs by hand.)
func processOutOfReach(pid uint32) bool {
	if selfElevated {
		return false // we can reach anything we can see
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false // gone, or not ours to judge — say nothing rather than cry wolf
	}
	defer windows.CloseHandle(h)
	var tok windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok); err != nil {
		return true // visible but its token is closed to us: it is above us
	}
	defer tok.Close()
	return tok.IsElevated()
}

// windowOutOfReach reports whether hwnd belongs to such a process.
func windowOutOfReach(hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}
	var pid uint32
	_, _, _ = w32ProcGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return false
	}
	return processOutOfReach(pid)
}

// inputBlocker names the window that is swallowing remote input, or "".
//
// The FOREGROUND window is what decides, whatever the viewer was aiming at:
// while an elevated window has focus, every injected event is dropped, so a
// click meant for an ordinary window beside it disappears just the same. The
// target is checked second, for the case where the user is aiming AT the
// elevated window while something reachable holds focus — the click would land
// nowhere either way, and naming what they clicked is the more useful answer.
func inputBlocker(targetID string) string {
	fg, _, _ := w32ProcGetForegroundWindow.Call()
	if windowOutOfReach(fg) {
		return describeWindow(fg)
	}
	if hwnd, err := w32ParseHWND(targetID); err == nil && windowOutOfReach(hwnd) {
		return describeWindow(hwnd)
	}
	return ""
}

// describeWindow names a window for a human: its title, falling back to the
// executable behind it.
func describeWindow(hwnd uintptr) string {
	if t := strings.TrimSpace(w32WindowText(hwnd)); t != "" {
		return t
	}
	var pid uint32
	_, _, _ = w32ProcGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if n := w32ProcessName(pid); n != "" {
		return n
	}
	return "an elevated window"
}

// gaRoot is GetAncestor's GA_ROOT: the top-level window a child belongs to.
const gaRoot = 2

// w32UnblockForeground breaks the deadlock an elevated window creates for the
// DESKTOP view, where there is no target window to raise and so nothing to
// dislodge it (win32Windows.focus is a no-op for displays).
//
// The deadlock: while an elevated window holds the foreground, every injected
// event is discarded, so the click that would ordinarily raise the window under
// the cursor never arrives. The whole desktop stops responding — not one window
// — and no amount of clicking can recover it, because clicking is the thing
// that stopped working.
//
// The way out is that only INJECTION is restricted. Changing the foreground is
// not: raising a reachable window succeeds even while an elevated one holds it,
// which was confirmed on the reporting machine (an elevated System Properties
// held focus; raising Chrome took it, and input started landing again).
//
// So: when the foreground is out of reach, raise whatever reachable window the
// click is aimed at, and let the click follow. Only ever called when input
// would otherwise be dropped — with nothing blocking, an injected click already
// activates what it lands on, and interfering would just fight the user.
// Clicking the elevated window ITSELF stays impossible; Windows means it.
func w32UnblockForeground(x, y int) {
	fg, _, _ := w32ProcGetForegroundWindow.Call()
	if fg == 0 || !windowOutOfReach(fg) {
		return
	}
	hwnd, _, _ := w32ProcWindowFromPoint.Call(uintptr(uint32(int32(x))) | uintptr(uint32(int32(y)))<<32)
	if hwnd == 0 {
		return
	}
	if root, _, _ := w32ProcGetAncestor.Call(hwnd, gaRoot); root != 0 {
		hwnd = root
	}
	if hwnd == fg || windowOutOfReach(hwnd) {
		return // aimed at the blocker itself: nothing to do, and nothing that can be done
	}
	w32RaiseHWND(hwnd)
}
