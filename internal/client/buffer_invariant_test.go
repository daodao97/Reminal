// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"strings"
	"testing"
)

// TestScrollbackByteAccounting is a property test for the invariant that drives the
// scrollback's memory bound: s.bytes must always equal the sum of the retained
// entries' Data lengths, and eviction must keep s.bytes within maxBytes. A drift
// here would mean wrong eviction — either a memory leak (under-count → never evict)
// or premature drops (over-count). Resize markers (empty Data) and bar chrome are
// mixed in to exercise the zero-length and non-data cases.
func TestScrollbackByteAccounting(t *testing.T) {
	sb := newScrollback(4096) // small cap forces frequent eviction

	sumData := func() int {
		total := 0
		for _, e := range sb.entries {
			total += len(e.Data)
		}
		return total
	}

	// Deterministic LCG so failures reproduce.
	seed := 12345
	next := func(n int) int {
		seed = (seed*1103515245 + 12345) & 0x7fffffff
		return seed % n
	}

	for i := 0; i < 20000; i++ {
		switch next(4) {
		case 0, 1:
			sb.Append(strings.Repeat("x", next(300)))
		case 2:
			sb.AppendResize(1+next(200), 1+next(80)) // empty Data — must not move s.bytes
		case 3:
			sb.AppendBar(strings.Repeat("b", next(100)))
		}
		if sb.bytes != sumData() {
			t.Fatalf("iter %d: s.bytes=%d != Σlen(Data)=%d (accounting drift)", i, sb.bytes, sumData())
		}
		if sb.bytes > sb.maxBytes && len(sb.entries) > 1 {
			t.Fatalf("iter %d: bytes=%d over cap=%d with %d entries (eviction failed)",
				i, sb.bytes, sb.maxBytes, len(sb.entries))
		}
	}
}
