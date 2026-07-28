// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build linux

package client

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// fillHostInfo reads Linux stats from /proc. Best-effort throughout.
func fillHostInfo(h *HostInfo) {
	h.CPUModel = linuxCPUModel()
	linuxMem(h)
	linuxUptime(h)
	linuxLoad(h)
}

func linuxCPUModel() string {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "model name") {
			if i := strings.IndexByte(line, ':'); i >= 0 {
				return strings.TrimSpace(line[i+1:])
			}
		}
	}
	return ""
}

func linuxMem(h *HostInfo) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return
	}
	defer f.Close()
	var total, avail uint64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		kb, _ := strconv.ParseUint(fields[1], 10, 64) // value is in kB
		switch fields[0] {
		case "MemTotal:":
			total = kb * 1024
		case "MemAvailable:":
			avail = kb * 1024
		}
	}
	h.MemTotal = total
	if total > 0 && avail > 0 && avail <= total {
		h.MemUsed = total - avail
	}
}

func linuxUptime(h *HostInfo) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return
	}
	fields := strings.Fields(string(data))
	if len(fields) >= 1 {
		if secs, err := strconv.ParseFloat(fields[0], 64); err == nil {
			h.Uptime = int64(secs)
		}
	}
}

func linuxLoad(h *HostInfo) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return
	}
	fields := strings.Fields(string(data))
	if len(fields) >= 3 {
		h.Load1, _ = strconv.ParseFloat(fields[0], 64)
		h.Load5, _ = strconv.ParseFloat(fields[1], 64)
		h.Load15, _ = strconv.ParseFloat(fields[2], 64)
	}
}
