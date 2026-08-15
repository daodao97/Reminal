// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build windows

package client

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	nudgeKernel32            = windows.NewLazySystemDLL("kernel32.dll")
	procWriteConsoleInputW   = nudgeKernel32.NewProc("WriteConsoleInputW")
	procFlushConsoleInputBuf = nudgeKernel32.NewProc("FlushConsoleInputBuffer")
)

// flushConsoleStdin discards everything pending on the console input queue —
// including the nudge ENTER if pumpHostStdin exited without consuming it.
// Called after the nudge has had a moment to land, so the stray keystroke can
// never reach the shell through the viewer that takes stdin over next.
func flushConsoleStdin() {
	_, _, _ = procFlushConsoleInputBuf.Call(uintptr(windows.Handle(os.Stdin.Fd())))
}

// inputRecordKey is INPUT_RECORD carrying a KEY_EVENT_RECORD — laid out
// manually since x/sys doesn't export it.
type inputRecordKey struct {
	eventType uint16
	_         uint16 // alignment
	keyDown   int32
	repeat    uint16
	vk        uint16
	scan      uint16
	char      uint16
	ctrl      uint32
}

// nudgeConsoleStdin queues a synthetic ENTER key event on this process's own
// console input. Purpose: pumpHostStdin sits in a blocking console read; a
// hot restart needs that read to complete NOW so the pump can observe the
// restarting flag and stop competing for stdin with the in-process viewer.
// The pump discards the woken read's data, so the fake keystroke never
// reaches the shell. Best-effort: on failure the pending read swallows the
// user's next real keystroke instead — degraded, not broken.
func nudgeConsoleStdin() {
	h := windows.Handle(os.Stdin.Fd())
	const keyEvent = 0x0001
	const vkReturn = 0x0D
	recs := []inputRecordKey{
		{eventType: keyEvent, keyDown: 1, repeat: 1, vk: vkReturn, scan: 0x1C, char: '\r'},
		{eventType: keyEvent, keyDown: 0, repeat: 1, vk: vkReturn, scan: 0x1C, char: '\r'},
	}
	var written uint32
	_, _, _ = procWriteConsoleInputW.Call(
		uintptr(h),
		uintptr(unsafe.Pointer(&recs[0])),
		uintptr(len(recs)),
		uintptr(unsafe.Pointer(&written)),
	)
}
