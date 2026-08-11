// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"regexp"
	"strings"
)

// The reconnect scrollback-duplication bug: inline TUIs (Claude Code / Ink) repaint
// their ENTIRE visible transcript on a SIGWINCH resize, and because the app pre-wraps
// its own output (it emits each row already broken to the terminal width — verified: no
// emitted run ever exceeds the column count), each repaint re-emits a full copy of recent
// history, re-wrapped to the new width. Over a session of resizes/reconnects a whole
// transcript block can end up stamped many times over.
//
// dedupBlocks removes those re-emitted copies from rebuilt history. It works on whole
// PARAGRAPHS (maximal runs of non-blank rows), compared by their alphanumeric word
// sequence:
//
//   - WORD-level, not line-level, because the app re-wraps on resize: the 119-col copy
//     and the 58-col copy of a paragraph have different line breaks but identical words.
//   - Alphanumeric-only, so per-line chrome (the `▎` quote-bar, `---`, tree glyphs) —
//     which lands at wrap-dependent positions — is dropped and doesn't defeat the match.
//   - PARAGRAPH granularity (not sliding word-runs) so a single drifting line inside a
//     frame (a spinner, a token counter) only spares its own paragraph, and — crucially —
//     so we never collapse a short phrase that legitimately recurs. Real sessions repeat
//     text (successive drafts, retried commands); only a substantial VERBATIM paragraph
//     repeating is an unambiguous re-emit.
//
// It keeps the FIRST occurrence (the copy in its original scrollback position, preserving
// reading order) and drops later stamped copies. Conservative: a paragraph shorter than
// dedupMinParaWords is never deduped, so headings, prompts, and one-liners always survive.
const dedupMinParaWords = 12

var (
	dedupANSI = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]|\x1b\][^\x07]*(?:\x07|\x1b\\)`)
	dedupWord = regexp.MustCompile(`[\p{L}\p{N}]+`)
)

// dedupWords lowercases a (possibly styled) line and returns only its alphanumeric word
// tokens — the wrap-invariant content of the line.
func dedupWords(s string) []string {
	return dedupWord.FindAllString(strings.ToLower(dedupANSI.ReplaceAllString(s, "")), -1)
}

func dedupBlank(s string) bool {
	return strings.TrimSpace(dedupANSI.ReplaceAllString(s, "")) == ""
}

// dedupBlocks returns history with later verbatim copies of substantial paragraphs
// removed. Blank separators around a dropped paragraph are absorbed so the surviving
// history doesn't accumulate blank gaps.
func dedupBlocks(lines []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(lines))
	i := 0
	for i < len(lines) {
		if dedupBlank(lines[i]) {
			out = append(out, lines[i])
			i++
			continue
		}
		// Gather one paragraph: consecutive non-blank rows.
		j := i
		var words []string
		for j < len(lines) && !dedupBlank(lines[j]) {
			words = append(words, dedupWords(lines[j])...)
			j++
		}
		key := strings.Join(words, "\x00")
		if len(words) >= dedupMinParaWords && seen[key] {
			// Re-emitted copy: drop it and absorb the ONE trailing blank that followed
			// it, so the blank preceding it survives as the separator between its
			// neighbours (removing that leading blank instead would merge them).
			if j < len(lines) && dedupBlank(lines[j]) {
				j++
			}
			i = j
			continue
		}
		if len(words) >= dedupMinParaWords {
			seen[key] = true
		}
		out = append(out, lines[i:j]...)
		i = j
	}
	return out
}

// Frame-matched dedup: the second, width-INDEPENDENT half of the resize story.
//
// dedupBlocks above only collapses paragraphs re-emitted at the SAME width (identical
// word key). But an inline TUI repaints its frame on EVERY SIGWINCH — so a session
// that bounced between widths stamps copies of the CURRENT frame's content into
// committed scrollback re-wrapped differently each time. Those copies have identical
// words but different paragraph boundaries (blank/wrap structure shifts with width),
// so no paragraph-key scheme can pair them with each other.
//
// What CAN be paired is each copy against the live frame itself: every stale copy is,
// by definition, a re-emission of content the app is showing RIGHT NOW. So we match
// committed paragraphs against the current screen's word stream using word shingles
// (order-preserving 8-word windows — immune to wrapping, blank lines, and per-line
// chrome). A paragraph whose shingles are almost entirely present in the frame is a
// stale repaint copy: drop it. This is provably lossless — the dropped words are still
// painted, in the frame, at the snapshot's bottom; nothing the user could scroll to
// disappears. Content the app has scrolled PAST (no longer in its frame) is never
// touched, no matter how similar.
const (
	dedupFrameShingle  = 8   // words per shingle; < dedupMinParaWords so every eligible paragraph has several
	dedupFrameCoverage = 0.9 // fraction of a paragraph's shingles that must be in the frame to drop it
)

// dedupAgainstFrame drops committed paragraphs that are stale re-emissions of the
// app's current frame (see block comment above). screen is the live screen's rows.
func dedupAgainstFrame(history, screen []string) []string {
	var fw []string
	for _, r := range screen {
		fw = append(fw, dedupWords(r)...)
	}
	if len(fw) < dedupFrameShingle {
		return history
	}
	shingles := make(map[string]bool, len(fw))
	for i := 0; i+dedupFrameShingle <= len(fw); i++ {
		shingles[strings.Join(fw[i:i+dedupFrameShingle], "\x00")] = true
	}
	out := make([]string, 0, len(history))
	i := 0
	for i < len(history) {
		if dedupBlank(history[i]) {
			out = append(out, history[i])
			i++
			continue
		}
		j := i
		var words []string
		for j < len(history) && !dedupBlank(history[j]) {
			words = append(words, dedupWords(history[j])...)
			j++
		}
		if len(words) >= dedupMinParaWords {
			total, hit := 0, 0
			for k := 0; k+dedupFrameShingle <= len(words); k++ {
				total++
				if shingles[strings.Join(words[k:k+dedupFrameShingle], "\x00")] {
					hit++
				}
			}
			if total > 0 && float64(hit) >= dedupFrameCoverage*float64(total) {
				// Stale repaint copy of the live frame: drop it, absorbing the one
				// trailing blank (same separator logic as dedupBlocks).
				if j < len(history) && dedupBlank(history[j]) {
					j++
				}
				i = j
				continue
			}
		}
		out = append(out, history[i:j]...)
		i = j
	}
	return out
}
