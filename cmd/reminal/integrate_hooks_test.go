// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An agent that rewrites its settings and drops reminal's tag on its hook
// entries (Claude Code does, on sign-in) must not get every hook twice on
// the next integrate: the entries are ours by their command too. A person's
// own hooks are left alone.
func TestHooksStayOnceThroughARewrite(t *testing.T) {
	home := t.TempDir()
	exe := "/usr/local/bin/reminal"
	spec := &hookSpec{file: ".claude/settings.json", key: []string{"hooks"}, shape: shapeMatcher,
		events: []hookEvent{{Event: "Stop", State: "done"}, {Event: "Notification", State: "notify"}}}
	path := filepath.Join(home, spec.file)
	if err := applyHooks(spec, home, exe, false); err != nil {
		t.Fatal(err)
	}
	// The agent rewrites the file: our tags go, and the person adds a hook.
	b, _ := os.ReadFile(path)
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	h := root["hooks"].(map[string]any)
	tags := 0
	for _, list := range h {
		for _, e := range list.([]any) {
			if _, ok := e.(map[string]any)[hookMarker]; ok {
				delete(e.(map[string]any), hookMarker)
				tags++
			}
		}
	}
	if tags != 2 {
		t.Fatalf("expected 2 tagged entries to strip, found %d", tags)
	}
	h["Stop"] = append(h["Stop"].([]any), map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "say done"}}})
	nb, _ := json.Marshal(root)
	_ = os.WriteFile(path, nb, 0o600)

	for i := 0; i < 3; i++ {
		if err := applyHooks(spec, home, exe, false); err != nil {
			t.Fatal(err)
		}
	}
	b, _ = os.ReadFile(path)
	_ = json.Unmarshal(b, &root)
	h = root["hooks"].(map[string]any)
	if n := len(h["Stop"].([]any)); n != 2 {
		t.Errorf("Stop has %d entries, want ours once and the person's: %s", n, b)
	}
	if n := len(h["Notification"].([]any)); n != 1 {
		t.Errorf("Notification has %d entries, want 1: %s", n, b)
	}
	if !strings.Contains(string(b), "say done") {
		t.Error("the person's own hook was removed")
	}
}

// A person's own group that happens to run our command, with a matcher or a
// timeout of their own, is theirs and stays.
func TestPersonsGroupRunningOurCommandStays(t *testing.T) {
	theirs := map[string]any{"matcher": "permission_prompt", "hooks": []any{map[string]any{"type": "command", "command": "reminal hook notify", "timeout": 5.0}}}
	if ourHookEntry(theirs) {
		t.Fatal("a person's group with a matcher and a timeout was taken as ours")
	}
	ours := map[string]any{"matcher": "", "hooks": []any{map[string]any{"type": "command", "command": "/usr/local/bin/reminal hook notify"}}}
	if !ourHookEntry(ours) {
		t.Fatal("our own untagged group was not recognised")
	}
}

// Only a command that is exactly reminal's hook command is ours.
func TestOurHookCommandMatch(t *testing.T) {
	ours := []string{
		"/usr/local/bin/reminal hook done",
		"reminal hook working",
		"'/Applications/My Apps/reminal' hook notify",
		`"C:/Program Files/reminal/reminal.exe" hook input`,
		"C:/Users/h/reminal.exe hook done",
		"  /opt/reminal hook done  ",
	}
	theirs := []string{
		"echo reminal hook done",
		"notify-send 'reminal hook done'",
		"/usr/local/bin/reminal hook done && say hi",
		"/usr/local/bin/reminal hook schedule",
		"/usr/local/bin/reminal hooked done",
		"/usr/local/bin/reminal-watch hook done",
		"say done",
		"cleanup.sh;reminal hook done",
		"make&&reminal hook done",
		"true||reminal hook done",
		"`id`reminal hook done",
		"notreminal hook done",
		"/opt/foo-reminal hook done",
		`"$(rm -rf ~)/reminal" hook done`,
	}
	for _, c := range ours {
		if !reminalHookCmdRe.MatchString(c) {
			t.Errorf("our command not recognised: %q", c)
		}
	}
	for _, c := range theirs {
		if reminalHookCmdRe.MatchString(c) {
			t.Errorf("a command that is not ours matched: %q", c)
		}
	}
}

// Antigravity's hooks go under reminal's own name in its hooks file, never
// at the top (agy rejects such a file); entries an earlier reminal left at
// the top are taken out, the person's own sets are kept, and re-running does
// not add a second copy.
func TestAntigravityHooksUnderOurName(t *testing.T) {
	home := t.TempDir()
	exe := "/usr/local/bin/reminal"
	var spec *hookSpec
	for _, a := range agentTargets() {
		if a.Bin == "agy" {
			spec = a.hooks
		}
	}
	if spec == nil {
		t.Fatal("no agy hook spec")
	}
	path := filepath.Join(home, spec.file)
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	before := `{"Stop":[{"command":"/usr/local/bin/reminal hook done","reminalHook":"done"}],` +
		`"theirs":{"Stop":[{"command":"say done"}]}}`
	_ = os.WriteFile(path, []byte(before), 0o600)
	for i := 0; i < 3; i++ {
		if err := applyHooks(spec, home, exe, false); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(path)
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	if _, top := root["Stop"]; top {
		t.Errorf("an entry is left at the top of the file: %s", b)
	}
	ours, _ := root[mcpServerName].(map[string]any)
	for _, ev := range []string{"PreInvocation", "Stop"} {
		list, _ := ours[ev].([]any)
		if len(list) != 1 {
			t.Errorf("%s under %q has %d entries, want 1: %s", ev, mcpServerName, len(list), b)
		}
	}
	if !strings.Contains(string(b), `"say done"`) {
		t.Errorf("the person's own set was removed: %s", b)
	}
	if err := applyHooks(spec, home, exe, true); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if strings.Contains(string(b), "reminal hook") || !strings.Contains(string(b), `"say done"`) {
		t.Errorf("after --remove: %s", b)
	}
}

// An entry is written with a matcher only when its event names one: every
// event in the table writes none today, exactly as before the table carried
// matchers, and one that names tools gets them — and is still replaced, not
// added again, on a re-run.
func TestHookEntryMatcher(t *testing.T) {
	exe := "/usr/local/bin/reminal"
	for _, tg := range agentTargets() {
		if tg.hooks == nil {
			continue
		}
		for _, ev := range tg.hooks.events {
			if _, has := hookEntry(tg.hooks.shape, exe, ev)["matcher"]; has {
				t.Errorf("%s %s: entry carries a matcher", tg.Name, ev.Event)
			}
		}
	}

	home := t.TempDir()
	spec := &hookSpec{file: "s.json", key: []string{"hooks"}, shape: shapeMatcher,
		events: []hookEvent{{Event: "PostToolUse", State: "working", Matcher: "Bash|Edit"}}}
	for i := 0; i < 2; i++ {
		if err := applyHooks(spec, home, exe, false); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(home, spec.file))
	var root struct {
		Hooks map[string][]map[string]any `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	list := root.Hooks["PostToolUse"]
	if len(list) != 1 || list[0]["matcher"] != "Bash|Edit" {
		t.Errorf("want one entry with its matcher, got %s", b)
	}
}
