// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package main

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// Text past mcpTextBudget is cut off by Claude Code before the model sees it,
// silently: the server's instructions lost their last 60% that way, rules
// and all. Codex has a limit of its own: a tool whose input schema is over
// 5000 bytes (compact JSON) loses every parameter description.
const (
	mcpTextBudget   = 2048
	mcpSchemaBudget = 4500 // Codex's 5000, with room
)

func TestMCPTextFitsBudget(t *testing.T) {
	if n := utf8.RuneCountInString(mcpInstructions); n > mcpTextBudget {
		t.Errorf("mcpInstructions is %d characters; only the first %d reach the model", n, mcpTextBudget)
	}
	checkToolsFitBudget(t, mcpToolList())
}

// checkToolsFitBudget holds every tool description, parameter description
// and input schema to what a model is actually shown.
func checkToolsFitBudget(t *testing.T, tools []map[string]any) {
	t.Helper()
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		desc, _ := tool["description"].(string)
		if n := utf8.RuneCountInString(desc); n > mcpTextBudget {
			t.Errorf("%s: description is %d characters; only the first %d reach the model", name, n, mcpTextBudget)
		}
		schema, _ := tool["inputSchema"].(map[string]any)
		if b, _ := json.Marshal(schema); len(b) > mcpSchemaBudget {
			t.Errorf("%s: input schema is %d bytes; over %d Codex drops its parameter descriptions", name, len(b), mcpSchemaBudget)
		}
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
	desc := map[string]string{"instructions": mcpInstructions}
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
		if !strings.Contains(desc[c.where], c.text) {
			t.Errorf("%s no longer says %q", c.where, c.text)
		}
	}
}
