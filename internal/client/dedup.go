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
