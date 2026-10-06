// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Text past mcpTextBudget is cut off by Claude Code before the model sees it,
// silently: the server's instructions lost their last 60% that way, rules
// and all. Everything an agent is meant to read has to fit.
func TestMCPTextFitsBudget(t *testing.T) {
	if n := utf8.RuneCountInString(mcpInstructions); n > mcpTextBudget {
		t.Errorf("mcpInstructions is %d characters; only the first %d reach the model", n, mcpTextBudget)
	}
	for _, tool := range mcpToolList() {
		name, _ := tool["name"].(string)
		desc, _ := tool["description"].(string)
		if n := utf8.RuneCountInString(desc); n > mcpTextBudget {
			t.Errorf("%s: description is %d characters; only the first %d reach the model", name, n, mcpTextBudget)
		}
		schema, _ := tool["inputSchema"].(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		for p, v := range props {
			m, _ := v.(map[string]any)
			d, _ := m["description"].(string)
			if n := utf8.RuneCountInString(d); n > mcpTextBudget {
				t.Errorf("%s.%s: description is %d characters; only the first %d reach the model", name, p, n, mcpTextBudget)
			}
		}
	}
}

// The rules that left the instructions for a tool's own description must
// still be somewhere the model reads.
func TestMCPRulesHaveAHome(t *testing.T) {
	desc := map[string]string{}
	for _, tool := range mcpToolList() {
		name, _ := tool["name"].(string)
		desc[name], _ = tool["description"].(string)
	}
	for _, c := range []struct{ where, text string }{
		{"instructions", "report_issue"},
		{"instructions", "before asking them to recap"},
		{"send_keys", "swallow"},
		{"send_keys", "Never report text as sent"},
		{"add_note", "attention = you are BLOCKED"},
		{"add_note", "Only three"},
		{"read_replies", "pick the work back up"},
		{"list_sessions", "minutes_to_empty OR minutes_to_full"},
	} {
		in := desc[c.where]
		if c.where == "instructions" {
			in = mcpInstructions
		}
		if !strings.Contains(in, c.text) {
			t.Errorf("%s no longer says %q", c.where, c.text)
		}
	}
}
