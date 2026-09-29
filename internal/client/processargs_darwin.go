// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"bytes"
	"encoding/binary"

	"golang.org/x/sys/unix"
)

// processArgsNative reads pid's arguments from the kernel (KERN_PROCARGS2),
// each one whole — `ps -o args=` joins them with spaces, and an argument
// holding a space cannot be told apart from two. The buffer is argc (int32),
// the executable path, NUL padding, then argc NUL-terminated arguments.
func processArgsNative(pid int) ([]string, bool) {
	b, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(b) < 4 {
		return nil, false
	}
	argc := int(binary.LittleEndian.Uint32(b[:4]))
	rest := b[4:]
	i := bytes.IndexByte(rest, 0) // past the executable path
	if i < 0 {
		return nil, false
	}
	rest = rest[i:]
	for len(rest) > 0 && rest[0] == 0 { // and its padding
		rest = rest[1:]
	}
	args := make([]string, 0, argc)
	for len(args) < argc && len(rest) > 0 {
		j := bytes.IndexByte(rest, 0)
		if j < 0 {
			args = append(args, string(rest))
			break
		}
		args = append(args, string(rest[:j]))
		rest = rest[j+1:]
	}
	return args, len(args) > 0
}
