// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"strings"
	"testing"
)

// dfWrap wraps a word sequence at width w, one paragraph (no internal blanks).
func dfWrap(text string, w int) []string {
	return nsWrap(text, w)
}

// TestDedupAgainstFrame covers the cross-width resize-repaint case dedupBlocks
// can't pair: the same paragraph stamped into committed history at several widths
// (different wrap points → different paragraph keys) while the app still shows it
// in its live frame. All stamped copies must go; everything else must survive.
func TestDedupAgainstFrame(t *testing.T) {
	para := "the tide retreats and leaves behind a shallow basin of trapped seawater where anemones and hermit crabs wait out the hours until the ocean returns to reclaim them"
	old := "an entirely different committed passage about glaciers carving valleys over millennia that the app has long scrolled past and must never be touched by any dedup"

	// Committed history: an old unique paragraph, then the SAME para stamped at
	// three widths (as resize repaints do), blank-separated.
	var history []string
	history = append(history, dfWrap(old, 70)...)
	history = append(history, "")
	for _, w := range []int{44, 61, 78} {
		history = append(history, dfWrap(para, w)...)
		history = append(history, "")
	}

	// Live frame: shows para at the current width plus app chrome.
	screen := append([]string{"╭─ app chrome ─╮"}, dfWrap(para, 90)...)
	screen = append(screen, "│ > input box  │", "╰──────────────╯")

	out := dedupAgainstFrame(history, screen)
	joined := strings.Join(out, "\n")

	if !strings.Contains(joined, "glaciers carving valleys") {
		t.Errorf("unique committed paragraph was deleted:\n%s", joined)
	}
	if n := strings.Count(joined, "hermit crabs"); n != 0 {
		t.Errorf("stale frame re-emissions survived: %d copies still in history (want 0 — the frame itself paints it)\n%s", n, joined)
	}
}

// TestDedupAgainstFrameNeverTouchesScrolledPast: content similar to — but not a
// re-emission of — the frame must survive. A paragraph sharing SOME words with the
// frame (below shingle coverage) stays.
func TestDedupAgainstFrameNeverTouchesScrolledPast(t *testing.T) {
	frame := dfWrap("the quick brown fox jumps over the lazy dog and then runs far away into the deep dark forest never to be seen again by anyone", 60)
	// Shares vocabulary but different sequence → different shingles.
	past := dfWrap("the lazy dog watches the brown forest while the quick fox sleeps and anyone seen running away returns into the deep dark night again", 45)

	out := dedupAgainstFrame(append(append([]string{}, past...), ""), frame)
	if len(out) < len(past) {
		t.Errorf("similar-but-distinct paragraph was deleted (%d of %d lines survived)", len(out), len(past))
	}
}

// TestDedupAgainstFrameShortParagraphsSafe: paragraphs under the word gate are never
// dropped even if fully present in the frame (prompts, headings, one-liners recur
// legitimately).
func TestDedupAgainstFrameShortParagraphsSafe(t *testing.T) {
	frame := []string{"build passed all tests green deploy now ok done finished complete", "extra frame words to satisfy shingle minimum here now"}
	short := []string{"build passed all tests green"} // 5 words < dedupMinParaWords
	out := dedupAgainstFrame(append(append([]string{}, short...), ""), frame)
	if len(out) < 1 || out[0] != short[0] {
		t.Errorf("short paragraph dropped: %v", out)
	}
}
