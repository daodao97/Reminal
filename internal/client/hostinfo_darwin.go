// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build darwin

package client

import (
	"encoding/binary"
	"time"

	"golang.org/x/sys/unix"
)

// fillHostInfo reads macOS stats via sysctl. Each read is independent and
// best-effort — a failing one just leaves its field zero.
func fillHostInfo(h *HostInfo) {
	if s, err := unix.Sysctl("machdep.cpu.brand_string"); err == nil {
		h.CPUModel = s
	}
	if v, err := unix.SysctlUint64("hw.memsize"); err == nil {
		h.MemTotal = v
	}
	h.MemUsed = darwinMemUsed(h.MemTotal)

	// kern.boottime is a struct timeval; the first machine-word is the seconds.
	if raw, err := unix.SysctlRaw("kern.boottime"); err == nil && len(raw) >= 8 {
		sec := int64(binary.LittleEndian.Uint64(raw[:8]))
		if sec > 0 {
			h.Uptime = time.Now().Unix() - sec
		}
	}

	// vm.loadavg: struct loadavg { fixpt_t ldavg[3]; long fscale; }.
	// fixpt values are integers scaled by fscale; divide to get the float.
	if raw, err := unix.SysctlRaw("vm.loadavg"); err == nil && len(raw) >= 24 {
		l0 := binary.LittleEndian.Uint32(raw[0:4])
		l1 := binary.LittleEndian.Uint32(raw[4:8])
		l2 := binary.LittleEndian.Uint32(raw[8:12])
		scale := float64(binary.LittleEndian.Uint32(raw[16:20]))
		if scale > 0 {
			h.Load1 = float64(l0) / scale
			h.Load5 = float64(l1) / scale
			h.Load15 = float64(l2) / scale
		}
	}
}

// darwinMemUsed reports used memory the way Activity Monitor's "Memory Used"
// does: physical total minus what's instantly reclaimable — free pages plus
// file-backed (external) pages, i.e. the "Cached Files" the OS keeps around but
// can evict on demand. The previous formula subtracted only free+inactive
// pages, which left the (often huge) file cache counted as used — so a machine
// with 32 GB of cached files read as ~85% used when Activity Monitor showed
// ~37%. Best-effort; returns 0 on any failure.
func darwinMemUsed(total uint64) uint64 {
	pageSize, err := unix.SysctlUint32("vm.pagesize")
	if err != nil || pageSize == 0 {
		if ps, e := unix.SysctlUint64("hw.pagesize"); e == nil {
			pageSize = uint32(ps)
		}
	}
	if pageSize == 0 {
		return 0
	}
	free, e1 := unix.SysctlUint32("vm.page_free_count")
	if e1 != nil {
		return 0
	}
	avail := uint64(free)
	// File-backed pages are clean, evictable cache (Activity Monitor's "Cached
	// Files") — treat them as available, not used.
	if ext, e := unix.SysctlUint32("vm.page_pageable_external_count"); e == nil {
		avail += uint64(ext)
	} else if inactive, e := unix.SysctlUint32("vm.page_inactive_count"); e == nil {
		// Older kernel without the external counter: fall back to the rougher
		// free+inactive estimate rather than reporting nothing.
		avail += uint64(inactive)
	}
	availBytes := avail * uint64(pageSize)
	if availBytes >= total {
		return 0
	}
	return total - availBytes
}
