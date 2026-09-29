// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build windows

package client

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// processArgsNative reads another process's command line (Windows 8.1+:
// ProcessCommandLineInformation) and splits it the way the process itself
// would (CommandLineToArgvW).
func processArgsNative(pid int) ([]string, bool) {
	line := processCommandLine(uint32(pid))
	if line == "" {
		return nil, false
	}
	p, err := windows.UTF16PtrFromString(line)
	if err != nil {
		return nil, false
	}
	var argc int32
	argv, err := windows.CommandLineToArgv(p, &argc)
	if err != nil {
		return nil, false
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(argv)))
	args := make([]string, 0, argc)
	for i := 0; i < int(argc); i++ {
		args = append(args, windows.UTF16PtrToString(&argv[i][0]))
	}
	return args, len(args) > 0
}

func processCommandLine(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]byte, 64<<10)
	var n uint32
	if err := windows.NtQueryInformationProcess(h, windows.ProcessCommandLineInformation,
		unsafe.Pointer(&buf[0]), uint32(len(buf)), &n); err != nil {
		return ""
	}
	us := (*windows.NTUnicodeString)(unsafe.Pointer(&buf[0]))
	if us.Buffer == nil || us.Length == 0 {
		return ""
	}
	// Length is in bytes, and the string need not end in a NUL.
	return windows.UTF16ToString(unsafe.Slice(us.Buffer, us.Length/2))
}
