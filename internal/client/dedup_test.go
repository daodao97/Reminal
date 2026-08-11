// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// replayCapture runs the real Ink resize+repaint capture through the history rebuilder
// (vviewWriter) and returns the rebuilt rows.
func replayCapture(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile("testdata/scrollback_stable_resize.json")
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	var events []capEvent
	if err := json.Unmarshal(data, &events); err != nil {
		t.Fatalf("parse capture: %v", err)
	}
	e, w := newVView(t, 119, 51)
	w.tall = 400
	for _, ev := range events {
		switch ev.T {
		case "w":
			b, derr := base64.StdEncoding.DecodeString(ev.D)
			if derr != nil {
				t.Fatalf("bad base64: %v", derr)
			}
			w.Write(b)
		case "r":
			w.setGeometry(ev.C, ev.R)
		}
	}
	return vviewRows(e)
}

// paraKeys groups rows into paragraphs and returns the word-sequence key of each
// substantial (>= dedupMinParaWords) paragraph — the same notion dedupBlocks dedups on.
func paraKeys(rows []string) []string {
	var keys []string
	i := 0
	for i < len(rows) {
		if dedupBlank(rows[i]) {
			i++
			continue
		}
		var words []string
		for i < len(rows) && !dedupBlank(rows[i]) {
			words = append(words, dedupWords(rows[i])...)
			i++
		}
		if len(words) >= dedupMinParaWords {
			keys = append(keys, strings.Join(words, "\x00"))
		}
	}
	return keys
}

// TestDedupBlocksSafeAndEffective is the (B) oracle, using the CORRECT invariant for a
// heuristic that must never eat real content: after dedup, (1) NO substantial paragraph
// is duplicated, and (2) EVERY distinct paragraph that existed in the input still exists
// — nothing unique is lost. Needle-counting is deliberately NOT used as the oracle: this
// capture is a copywriting session that legitimately repeats phrases across drafts.
func TestDedupBlocksSafeAndEffective(t *testing.T) {
	rows := replayCapture(t)
	deduped := dedupBlocks(rows)

	before := paraKeys(rows)
	after := paraKeys(deduped)

	distinct := func(ks []string) map[string]int {
		m := map[string]int{}
		for _, k := range ks {
			m[k]++
		}
		return m
	}
	bset, aset := distinct(before), distinct(after)

	// (1) Effectiveness: no substantial paragraph survives more than once.
	dupsRemaining := 0
	for k, c := range aset {
		if c > 1 {
			dupsRemaining++
			t.Errorf("paragraph still duplicated %d× after dedup: %.60q…", c, strings.ReplaceAll(k, "\x00", " "))
		}
	}

	// (2) Safety: every distinct paragraph in the input is still present (no unique
	// content class was deleted — only surplus copies).
	lost := 0
	for k := range bset {
		if aset[k] == 0 {
			lost++
			t.Errorf("dedup DELETED a unique paragraph (content loss): %.60q…", strings.ReplaceAll(k, "\x00", " "))
		}
	}

	collapsed := len(before) - len(after)
	t.Logf("rows %d→%d  substantial paras %d→%d (distinct %d)  copies collapsed=%d  dupsRemaining=%d  lost=%d",
		len(rows), len(deduped), len(before), len(after), len(bset), collapsed, dupsRemaining, lost)

	// Concrete safety check: the two differently-worded 'people poke holes' paragraphs
	// (legitimate draft variants, NOT duplicates) must both survive.
	ansi := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	countLines := func(ls []string, needle string) int {
		c := 0
		for _, l := range ls {
			if strings.Contains(ansi.ReplaceAllString(l, ""), needle) {
				c++
			}
		}
		return c
	}
	if got := countLines(deduped, "people poke holes in the crypto"); got != countLines(rows, "people poke holes in the crypto") {
		t.Errorf("legitimate repeat was altered: 'people poke holes' %d→%d (must be preserved)",
			countLines(rows, "people poke holes in the crypto"), got)
	}
}
