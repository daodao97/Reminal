// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"strings"
	"testing"
)

// TestBaseAdvancesPastEvictedResizeMarker guards the geometry a snapshot rebuild
// starts its replay at (scrollback.Base()). When eviction drops a resize marker,
// baseCols/baseRows must advance to that marker's geometry — otherwise the rebuild
// replays the oldest retained (post-resize) lines at the pre-resize width and wraps
// them into garbage. This is "Path #3" of the reconnect-dup diagnosis; verified
// here to advance correctly (not a dup contributor).
func TestBaseAdvancesPastEvictedResizeMarker(t *testing.T) {
	sb := newScrollback(600)
	sb.SetBase(80, 24)
	sb.Append(strings.Repeat("a", 200)) // 80-col data
	sb.AppendResize(120, 40)            // geometry -> 120x40
	for i := 0; i < 5; i++ {            // enough 120-col data to evict the 80-col data AND the marker
		sb.Append(strings.Repeat("b", 200))
	}

	if len(sb.entries) == 0 || sb.entries[0].Cols > 0 {
		t.Fatalf("scenario setup: expected the marker evicted and oldest retained to be data, got entries=%d", len(sb.entries))
	}
	bc, br := sb.Base()
	if bc != 120 || br != 40 {
		t.Errorf("Base()=%dx%d after evicting the resize marker, want 120x40 (replay would start at the wrong width)", bc, br)
	}
}
