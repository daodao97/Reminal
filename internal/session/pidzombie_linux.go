// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build linux

package session

import (
	"os"
	"strconv"
	"strings"
)

// pidIsZombie reports whether pid is a zombie (defunct) process. /proc/<pid>/stat
// field 3 is the state char ('Z' for zombie). Field 2 (comm) is wrapped in
// parentheses and may itself contain spaces or ')', so scan past the LAST ')'
// and read the state that follows. Best-effort: any read error → not a zombie.
func pidIsZombie(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	// Layout after comm is ") S ..." — state char sits two bytes past the ')'.
	if i < 0 || i+2 >= len(s) {
		return false
	}
	return s[i+2] == 'Z'
}
