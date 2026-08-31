// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"encoding/json"
	"testing"
	"time"
)

// Every test here models the situation that actually breaks: SEVERAL
// `reminal mcp` publishers, each holding its own copy of the notes and each
// publishing the complete set. That is the normal state on a machine with more
// than one coding agent registered.

func note(id string, ts int64) windowNote {
	return windowNote{ID: id, Status: "attention", Title: id, TS: ts}
}

// TestStalePublisherCannotResurrect is the reported bug: a note cleared from a
// phone came back hours later, repeatedly, because a second publisher never
// learned about the dismissal and kept sending it.
func TestStalePublisherCannotResurrect(t *testing.T) {
	n := newNoteStore()
	old := time.Now().Add(-11 * time.Hour).Unix()

	// Publisher A publishes; the viewer sees the note.
	n.replace(map[string][]windowNote{"77": {note("n1", old)}})
	if got := len(n.snapshot()["77"]); got != 1 {
		t.Fatalf("setup: want 1 note, got %d", got)
	}

	// The user dismisses it. (handleNoteAct does this via the Agent; the store
	// side is the tombstone.)
	n.mu.Lock()
	n.dismissed["77|n1"] = time.Now().Unix()
	delete(n.byWin, "77")
	n.mu.Unlock()

	// Publisher B, which never saw the dismissal, publishes its stale set.
	n.replace(map[string][]windowNote{"77": {note("n1", old)}})
	if got := n.snapshot()["77"]; len(got) != 0 {
		t.Fatalf("dismissed note came back from a stale publisher: %+v", got)
	}
}

// TestClearAllThenStalePublisher covers the same thing for "dismiss all".
func TestClearAllThenStalePublisher(t *testing.T) {
	n := newNoteStore()
	old := time.Now().Add(-time.Hour).Unix()
	n.replace(map[string][]windowNote{"5": {note("a", old), note("b", old)}})

	n.mu.Lock()
	n.clearedAt["5"] = time.Now().Unix()
	delete(n.byWin, "5")
	n.mu.Unlock()

	n.replace(map[string][]windowNote{"5": {note("a", old), note("b", old)}})
	if got := n.snapshot()["5"]; len(got) != 0 {
		t.Fatalf("cleared window repopulated from a stale publisher: %+v", got)
	}
}

// TestDismissDoesNotSilenceLaterNotes is the other half, and the one a naive
// tombstone gets wrong: `add_note` is an upsert on note_id and re-stamps TS, so
// an agent legitimately raising the same id again must still be seen. A
// tombstone that ignored timestamps would mute that window forever.
func TestDismissDoesNotSilenceLaterNotes(t *testing.T) {
	n := newNoteStore()
	dismissedAt := time.Now().Add(-time.Minute).Unix()

	n.mu.Lock()
	n.dismissed["9|dup"] = dismissedAt
	n.clearedAt["9"] = dismissedAt
	n.mu.Unlock()

	// Same id, but raised again afterwards.
	fresh := note("dup", time.Now().Unix())
	n.replace(map[string][]windowNote{"9": {fresh}})
	if got := n.snapshot()["9"]; len(got) != 1 {
		t.Fatalf("a note raised after the dismissal was swallowed: %+v", got)
	}

	// And a brand-new note in a window that was cleared earlier.
	n.replace(map[string][]windowNote{"9": {note("brand-new", time.Now().Unix())}})
	if got := n.snapshot()["9"]; len(got) != 1 {
		t.Fatalf("a new note in a previously-cleared window was swallowed: %+v", got)
	}
}

// TestActsAreReadableByEveryPublisher is the root cause of the resurrection:
// the queue used to be drain-once, so the first publisher to poll consumed the
// only copy and the rest never learned anything.
func TestActsAreReadableByEveryPublisher(t *testing.T) {
	a := &Agent{notes: newNoteStore()}
	a.notes.mu.Lock()
	a.notes.seq++
	a.notes.acts = append(a.notes.acts, timedAct{
		noteAct: noteAct{Window: "3", ID: "x", Action: "dismiss", Seq: a.notes.seq},
		at:      time.Now(),
	})
	a.notes.mu.Unlock()

	for i, name := range []string{"publisher A", "publisher B", "publisher C"} {
		var got []noteAct
		if err := json.Unmarshal([]byte(a.recentActs()), &got); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(got) != 1 {
			t.Fatalf("%s (reader %d) saw %d actions, want 1 — reading must not consume", name, i+1, len(got))
		}
		if got[0].Seq == 0 {
			t.Errorf("%s: action has no sequence number, so a publisher can't tell new from already-applied", name)
		}
	}
}

// TestActsExpire keeps the log from growing without bound.
func TestActsExpire(t *testing.T) {
	a := &Agent{notes: newNoteStore()}
	a.notes.mu.Lock()
	a.notes.acts = append(a.notes.acts, timedAct{
		noteAct: noteAct{Window: "1", ID: "old", Action: "dismiss", Seq: 1},
		at:      time.Now().Add(-2 * actTTL),
	})
	a.notes.acts = append(a.notes.acts, timedAct{
		noteAct: noteAct{Window: "1", ID: "new", Action: "dismiss", Seq: 2},
		at:      time.Now(),
	})
	a.notes.mu.Unlock()

	var got []noteAct
	if err := json.Unmarshal([]byte(a.recentActs()), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "new" {
		t.Fatalf("expected only the recent action, got %+v", got)
	}
}

// TestTombstonesAreBounded — these outlive publishers by design, so they need a
// ceiling or a long-lived agent leaks memory one dismissal at a time.
func TestTombstonesAreBounded(t *testing.T) {
	n := newNoteStore()
	n.mu.Lock()
	now := time.Now().Unix()
	for i := 0; i < maxTombs+250; i++ {
		n.dismissed[string(rune('a'+i%26))+string(rune(i))] = now - int64(i)
	}
	n.pruneLocked()
	size := len(n.dismissed)
	n.mu.Unlock()
	if size > maxTombs {
		t.Errorf("dismissed grew to %d, cap is %d", size, maxTombs)
	}
}

// TestExpiredTombstoneIsForgotten — a tombstone older than any plausible
// publisher lifetime is dead weight and must not suppress forever.
func TestExpiredTombstoneIsForgotten(t *testing.T) {
	n := newNoteStore()
	ancient := time.Now().Add(-2 * tombTTL).Unix()
	n.mu.Lock()
	n.dismissed["2|zombie"] = ancient
	n.mu.Unlock()

	n.replace(map[string][]windowNote{"2": {note("zombie", ancient-1)}})
	if got := n.snapshot()["2"]; len(got) != 1 {
		t.Fatalf("expired tombstone still suppressing: %+v", got)
	}
}
