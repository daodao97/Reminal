// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build darwin

package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHelper installs a stand-in for reminal-capture that records the events
// it is given and answers them, so the protocol can be tested without posting
// a single key to whoever is at the machine.
func fakeKeysHelper(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "events.log")
	script := filepath.Join(dir, "reminal-capture")
	src := "#!/bin/sh\n[ \"$1\" = keys ] || exit 2\n" + strings.ReplaceAll(body, "{{LOG}}", log) + "\n"
	if err := os.WriteFile(script, []byte(src), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REMINAL_CAPTURE_HELPER", script)
	keysHelper.mu.Lock()
	keysHelper.stop()
	keysHelper.dead = false
	keysHelper.mu.Unlock()
	t.Cleanup(func() {
		keysHelper.mu.Lock()
		keysHelper.stop()
		keysHelper.dead = false
		keysHelper.mu.Unlock()
	})
	return log
}

// The whole point of the helper is that it STAYS running: a process per
// keystroke is what made typing lag. Several keys must go to one process.
func TestKeyHelperSendsEveryEventToOneRunningHelper(t *testing.T) {
	log := fakeKeysHelper(t, `while read -r line; do echo "$$ $line" >> {{LOG}}; echo ok; done`)

	if !keyHelperType("hello") {
		t.Fatal("typing text through the helper failed")
	}
	if !keyHelperKey(36, nil) { // return
		t.Fatal("pressing a key through the helper failed")
	}
	if !keyHelperKey(8, []string{"cmd"}) { // cmd+c
		t.Fatal("pressing a chord through the helper failed")
	}

	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("helper recorded nothing: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 3 {
		t.Fatalf("helper saw %d events, want 3", len(lines))
	}
	// Each line is "<pid> <json>". One pid for all three is the whole point:
	// a process per keystroke is the cost this exists to remove, and counting
	// events alone cannot tell the two apart.
	pids := map[string]bool{}
	for i, l := range lines {
		pid, body, found := strings.Cut(l, " ")
		if !found {
			t.Fatalf("line %d has no pid: %q", i+1, l)
		}
		pids[pid] = true
		lines[i] = body
	}
	if len(pids) != 1 {
		t.Fatalf("three keystrokes went to %d helper processes, want 1 — that is a spawn per keystroke", len(pids))
	}
	var ev map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &ev); err != nil {
		t.Fatalf("event 1 is not JSON: %v", err)
	}
	if ev["t"] != "text" || ev["s"] != "hello" {
		t.Errorf("event 1 = %v, want the text hello", ev)
	}
	if err := json.Unmarshal([]byte(lines[2]), &ev); err != nil {
		t.Fatalf("event 3 is not JSON: %v", err)
	}
	// cmd is bit 20 of CGEventFlags; a wrong bit here is a key that lands
	// without its modifier, which is worse than not injecting at all.
	if ev["code"].(float64) != 8 || uint64(ev["flags"].(float64)) != cgFlagCommand {
		t.Errorf("event 3 = %v, want keycode 8 with the command flag (%d)", ev, cgFlagCommand)
	}
}

// Every modifier has to map to its own bit, or a chord injects the wrong one.
func TestKeyHelperModifierBits(t *testing.T) {
	log := fakeKeysHelper(t, `while read -r line; do echo "$line" >> {{LOG}}; echo ok; done`)
	if !keyHelperKey(0, []string{"cmd", "shift", "ctrl", "alt"}) {
		t.Fatal("send failed")
	}
	raw, _ := os.ReadFile(log)
	var ev map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &ev); err != nil {
		t.Fatal(err)
	}
	want := uint64(cgFlagCommand | cgFlagShift | cgFlagControl | cgFlagOption)
	if got := uint64(ev["flags"].(float64)); got != want {
		t.Fatalf("flags = %d, want %d", got, want)
	}
}

// A helper that cannot run must not swallow the keystroke: the caller needs a
// false so it can fall back to osascript, which is slow but works.
func TestKeyHelperReportsFailureSoTheCallerCanFallBack(t *testing.T) {
	fakeKeysHelper(t, `exit 1`)
	if keyHelperType("hello") {
		t.Fatal("a helper that exits immediately reported success")
	}
}

// Same when the helper is answering something other than ok.
func TestKeyHelperRejectsAnUnexpectedReply(t *testing.T) {
	fakeKeysHelper(t, `while read -r line; do echo nope; done`)
	if keyHelperType("hello") {
		t.Fatal("a helper answering 'nope' reported success")
	}
}
