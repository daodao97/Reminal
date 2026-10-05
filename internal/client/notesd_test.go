// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

// These drive the real service over a real socket, because the bugs this
// replaces were entirely about what happens BETWEEN processes. A test that
// called the store directly would have passed against the broken design too.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// startNotesService brings the daemon's service up on a private socket and
// points the client helpers at it.
func startNotesService(t *testing.T) *notesDaemon {
	t.Helper()
	// NOT t.TempDir(): macOS caps unix socket paths at 104 bytes and t.TempDir()
	// embeds the test's name, so a descriptively-named test silently fails to
	// listen. Short prefix, explicit cleanup.
	dir, err := os.MkdirTemp("", "rn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "notes.sock")
	t.Setenv("REMINAL_NOTES_SOCK", sock)
	// A helper binary must never be found: these tests assert the store works
	// on a host with no badge, which is every Linux and Windows machine.
	t.Setenv("REMINAL_OVERLAY_BIN", filepath.Join(dir, "does-not-exist"))

	d := newNotesDaemon()
	stop := make(chan struct{})
	go serveNotesOn(d, stop)
	t.Cleanup(func() { close(stop) })

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if NotesDaemonReachable() {
			return d
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("notes service never came up")
	return nil
}

// TestNotesSurviveMissingBadge is the regression for losing a note entirely
// when the overlay helper isn't there — which used to make the whole feature
// unavailable off macOS, even though mirroring to a phone needs no badge.
func TestNotesSurviveMissingBadge(t *testing.T) {
	d := startNotesService(t)

	warn, err := NotesAdd(42, NoteInput{ID: "a", Status: "info", Title: "hello", TS: time.Now().Unix()})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if warn == "" {
		t.Error("expected a warning that the badge could not be drawn")
	}
	got := d.snapshot()
	if len(got["42"]) != 1 || got["42"][0].Title != "hello" {
		t.Fatalf("note was not stored despite the badge failing: %+v", got)
	}
}

// TestOnePublisherCannotClobberAnother is the whole point of moving the store
// into the daemon. Two MCP clients post to different windows; with per-process
// stores, whichever published last erased the other's window.
func TestOnePublisherCannotClobberAnother(t *testing.T) {
	d := startNotesService(t)

	if _, err := NotesAdd(1, NoteInput{ID: "from-A", Title: "A", TS: time.Now().Unix()}); err != nil {
		t.Fatalf("publisher A: %v", err)
	}
	if _, err := NotesAdd(2, NoteInput{ID: "from-B", Title: "B", TS: time.Now().Unix()}); err != nil {
		t.Fatalf("publisher B: %v", err)
	}

	got := d.snapshot()
	if len(got["1"]) != 1 || len(got["2"]) != 1 {
		t.Fatalf("one publisher erased the other: %+v", got)
	}
}

// TestDismissIsGlobal — a viewer dismissal reaches the one store, so there is no
// other copy left to put the note back. This is the reported bug, at the level
// where it is actually fixed rather than mitigated.
func TestDismissIsGlobal(t *testing.T) {
	d := startNotesService(t)
	if _, err := NotesAdd(7, NoteInput{ID: "n1", Title: "please look", TS: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}

	notesForwardAct("7", "n1", "dismiss")

	if got := d.snapshot(); len(got["7"]) != 0 {
		t.Fatalf("note survived the dismissal: %+v", got)
	}
	// And a second publisher re-posting its stale idea of the world cannot
	// bring it back, because there IS no second copy — it would have to call
	// add, which is a deliberate new note.
	if _, err := NotesAdd(7, NoteInput{ID: "n2", Title: "new one", TS: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}
	if got := d.snapshot(); len(got["7"]) != 1 || got["7"][0].ID != "n2" {
		t.Fatalf("expected only the new note: %+v", got)
	}
}

// TestRepliesAreALogNotAQueue is the root cause of the resurrection: with a
// drain-once queue the first reader took the only copy and every other MCP
// client was left blind.
func TestRepliesAreALogNotAQueue(t *testing.T) {
	startNotesService(t)
	if _, err := NotesAdd(9, NoteInput{ID: "x", Title: "t", TS: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}
	notesForwardAct("9", "x", "handback")

	// Three independent clients, each with its own cursor.
	for _, name := range []string{"client A", "client B", "client C"} {
		evs, seq, err := NotesReplies(0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(evs) == 0 {
			t.Fatalf("%s saw no replies — reading must not consume them", name)
		}
		if seq == 0 {
			t.Errorf("%s got no sequence back, so it cannot advance its cursor", name)
		}
	}

	// A client that has already consumed everything sees nothing new.
	_, seq, err := NotesReplies(0)
	if err != nil {
		t.Fatal(err)
	}
	evs, _, err := NotesReplies(seq)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 0 {
		t.Errorf("cursor did not suppress already-seen replies: %+v", evs)
	}
}

func TestClearRemovesTheWholeWindow(t *testing.T) {
	d := startNotesService(t)
	now := time.Now().Unix()
	for _, id := range []string{"a", "b", "c"} {
		if _, err := NotesAdd(3, NoteInput{ID: id, Title: id, TS: now}); err != nil {
			t.Fatal(err)
		}
	}
	if got := d.snapshot(); len(got["3"]) != 3 {
		t.Fatalf("setup: %+v", got)
	}
	if err := NotesClear(3); err != nil {
		t.Fatal(err)
	}
	if got := d.snapshot(); len(got["3"]) != 0 {
		t.Fatalf("clear left notes behind: %+v", got)
	}
}

// TestAddUpsertsOnID — an agent re-raising the same note_id means "update
// this", not "add a duplicate".
func TestAddUpsertsOnID(t *testing.T) {
	d := startNotesService(t)
	now := time.Now().Unix()
	if _, err := NotesAdd(4, NoteInput{ID: "same", Title: "first", TS: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := NotesAdd(4, NoteInput{ID: "same", Title: "second", TS: now + 1}); err != nil {
		t.Fatal(err)
	}
	got := d.snapshot()["4"]
	if len(got) != 1 {
		t.Fatalf("expected one note after upsert, got %d: %+v", len(got), got)
	}
	if got[0].Title != "second" {
		t.Errorf("upsert did not update the note: %+v", got[0])
	}
}

func TestWindowCapIsEnforced(t *testing.T) {
	startNotesService(t)
	now := time.Now().Unix()
	for i := 0; i < maxNoteWindows; i++ {
		if _, err := NotesAdd(uint32(100+i), NoteInput{ID: "n", Title: "t", TS: now}); err != nil {
			t.Fatalf("window %d: %v", i, err)
		}
	}
	if _, err := NotesAdd(999, NoteInput{ID: "n", Title: "t", TS: now}); err == nil {
		t.Errorf("expected the %d-window cap to be enforced", maxNoteWindows)
	}
}

// TestBadgeDetachesWhenEmptied — a window with nothing left to show must be
// detached from the helper, or an empty badge lingers on screen and `attached`
// grows for the life of the daemon.
func TestBadgeDetachesWhenEmptied(t *testing.T) {
	d := startNotesService(t)
	now := time.Now().Unix()
	if _, err := NotesAdd(11, NoteInput{ID: "only", Title: "t", TS: now}); err != nil {
		t.Fatal(err)
	}
	// Mark it attached the way a working helper would, so the detach path runs.
	d.mu.Lock()
	d.attached[11] = true
	d.mu.Unlock()

	if err := NotesRemove(11, "only"); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	stillAttached := d.attached[11]
	d.mu.Unlock()
	if stillAttached {
		t.Error("window still attached after its last note was removed")
	}

	// Same for a whole-window clear.
	if _, err := NotesAdd(12, NoteInput{ID: "a", Title: "t", TS: now}); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	d.attached[12] = true
	d.mu.Unlock()
	if err := NotesClear(12); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	stillAttached = d.attached[12]
	d.mu.Unlock()
	if stillAttached {
		t.Error("window still attached after clear_notes")
	}
}

// Notes outlive the daemon: the store is on disk, reloaded by the next daemon,
// with who owned each window; a folded (badge-evicted) note is kept; a window
// the helper reports closed is dropped.
func TestNotesPersistAcrossDaemonRestart(t *testing.T) {
	dir, err := os.MkdirTemp("", "rn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("REMINAL_OVERLAY_BIN", filepath.Join(dir, "does-not-exist"))
	path := filepath.Join(dir, "notes.json")

	d1 := newNotesDaemon()
	d1.path = path
	if _, err := d1.add(42, windowNote{ID: "a", Status: "attention", Title: "look here", TS: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := d1.add(42, windowNote{ID: "b", Status: "info", Title: "and here", TS: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := d1.add(7, windowNote{ID: "c", Status: "done", Title: "other window", TS: 3}); err != nil {
		t.Fatal(err)
	}
	// What the helper would have told it about window 42's owner, and that the
	// badge folded note "a" past its cap.
	d1.readBadgeEvents(io.NopCloser(strings.NewReader(
		`{"event":"attached","window":42,"pid":4242,"title":"Editor"}` + "\n" +
			`{"event":"evicted","window":42,"id":"a"}` + "\n")))
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("store not written: %v", err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("store mode %o", st.Mode().Perm())
	}

	// "The daemon restarts."
	d2 := newNotesDaemon()
	d2.path = path
	d2.load()
	snap := d2.snapshot()
	if len(snap["42"]) != 2 || len(snap["7"]) != 1 {
		t.Fatalf("after reload: %+v", snap)
	}
	var folded bool
	for _, n := range snap["42"] {
		if n.ID == "a" {
			folded = n.Folded
		}
	}
	if !folded {
		t.Fatal("the folded note came back unfolded, or was lost")
	}
	if o := d2.owners[42]; o.PID != 4242 || o.Title != "Editor" {
		t.Fatalf("owner not kept: %+v", o)
	}
	// The window really closed (the helper's rule): its notes go, the other's stay.
	d2.readBadgeEvents(io.NopCloser(strings.NewReader(`{"event":"closed","window":42}` + "\n")))
	snap = d2.snapshot()
	if len(snap["42"]) != 0 || len(snap["7"]) != 1 {
		t.Fatalf("after close: %+v", snap)
	}
	d3 := newNotesDaemon()
	d3.path = path
	d3.load()
	if s := d3.snapshot(); len(s["42"]) != 0 || len(s["7"]) != 1 {
		t.Fatalf("close not saved: %+v", s)
	}
	// Dismissing the last note forgets the window and its owner on disk too.
	d3.remove(7, "c")
	d4 := newNotesDaemon()
	d4.path = path
	d4.load()
	if s := d4.snapshot(); len(s) != 0 || len(d4.owners) != 0 {
		t.Fatalf("after dismiss: %+v %+v", s, d4.owners)
	}
}

// A store with no path (how every earlier test runs) still works in memory.
func TestNotesWithoutStoreStayInMemory(t *testing.T) {
	d := newNotesDaemon()
	t.Setenv("REMINAL_OVERLAY_BIN", "/does/not/exist")
	if _, err := d.add(1, windowNote{ID: "x", Status: "info", Title: "t"}); err != nil {
		t.Fatal(err)
	}
	if len(d.snapshot()["1"]) != 1 {
		t.Fatal("note lost")
	}
}

// Reviewer's case: many clients adding at once; the file must never end up
// behind memory (two saves landing in reverse order).
func TestNotesConcurrentSavesNeverLagMemory(t *testing.T) {
	dir, err := os.MkdirTemp("", "rn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("REMINAL_OVERLAY_BIN", filepath.Join(dir, "does-not-exist"))
	for iter := 0; iter < 20; iter++ {
		d := newNotesDaemon()
		d.path = filepath.Join(dir, fmt.Sprintf("notes-%d.json", iter))
		var wg sync.WaitGroup
		for i := 0; i < 48; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, _ = d.add(uint32(1+i%4), windowNote{ID: fmt.Sprintf("n%d", i), Status: "info", Title: "t"})
			}(i)
		}
		wg.Wait()
		inMem := 0
		for _, l := range d.snapshot() {
			inMem += len(l)
		}
		d2 := newNotesDaemon()
		d2.path = d.path
		d2.load()
		onDisk := 0
		for _, l := range d2.snapshot() {
			onDisk += len(l)
		}
		if onDisk != inMem {
			t.Fatalf("iteration %d: %d in memory, %d on disk", iter, inMem, onDisk)
		}
	}
}

// A closed window's notes are hidden at once, kept for the grace period, and
// revived by a new note; after the grace they are gone, across a restart too.
func TestNotesClosedGrace(t *testing.T) {
	dir, _ := os.MkdirTemp("", "rn")
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("REMINAL_OVERLAY_BIN", filepath.Join(dir, "does-not-exist"))
	d := newNotesDaemon()
	d.path = filepath.Join(dir, "notes.json")
	_, _ = d.add(5, windowNote{ID: "a", Status: "info", Title: "t"})
	d.readBadgeEvents(io.NopCloser(strings.NewReader(`{"event":"closed","window":5}` + "\n")))
	if len(d.snapshot()) != 0 {
		t.Fatal("closed window still visible")
	}
	if _, kept := d.notes[5]; !kept {
		t.Fatal("notes dropped at once instead of kept for the grace period")
	}
	_, _ = d.add(5, windowNote{ID: "b", Status: "info", Title: "back"})
	if len(d.snapshot()["5"]) != 2 {
		t.Fatalf("revived window should show both notes: %+v", d.snapshot())
	}
	d.readBadgeEvents(io.NopCloser(strings.NewReader(`{"event":"closed","window":5}` + "\n")))
	d.mu.Lock()
	d.closedAt[5] = time.Now().Add(-2 * closedGrace)
	d.mu.Unlock()
	d.save()
	d2 := newNotesDaemon()
	d2.path = d.path
	d2.load()
	if _, kept := d2.notes[5]; kept {
		t.Fatal("notes of a window closed past the grace came back after a restart")
	}
	if !d.sweepClosed() || len(d.notes) != 0 {
		t.Fatal("sweep did not drop the closed window")
	}
}

// A damaged store is set aside, never written over.
func TestNotesCorruptStoreSetAside(t *testing.T) {
	dir, _ := os.MkdirTemp("", "rn")
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	p := filepath.Join(dir, "notes.json")
	_ = os.WriteFile(p, []byte("{not json"), 0o600)
	d := newNotesDaemon()
	d.path = p
	d.load()
	m, _ := filepath.Glob(p + ".corrupt-*")
	if len(m) != 1 {
		t.Fatalf("corrupt store not set aside: %v", m)
	}
	if _, err := os.Stat(p); err == nil {
		t.Fatal("corrupt file still in place")
	}
}
