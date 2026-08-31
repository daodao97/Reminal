// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

// Window notes — the same annotations the desktop badge shows, mirrored to web
// viewers so a phone sees what the Mac does.
//
// Why the agent holds them at all: the notes are created by `reminal mcp`, which
// an agent's MCP client (Claude Code, Codex, …) spawns in a completely separate
// process tree from the reminal session serving the viewer. `reminal mcp`
// publishes them over the per-agent control socket, every local agent stores
// them, and each pushes to its own viewers.
//
// Scope is the MACHINE, not the session: a note belongs to a window, and a
// window belongs to the machine. Two sessions open on the same Mac both show
// the same window's notes, which is exactly what the badge on screen does.
//
// Lifetime matches the badge too — nothing is persisted. A window that closes
// takes its notes with it.

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/reminal/reminal/internal/protocol"
)

// windowNote is one annotation. Mirrors the overlay helper's model; `Status` is
// one of attention|working|info|done|handback.
type windowNote struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Title  string `json:"title"`
	Body   string `json:"body,omitempty"`
	Author string `json:"author,omitempty"`
	TS     int64  `json:"ts,omitempty"`
}

// noteStore is the machine's current annotations, keyed by CGWindowID as a
// string (JSON object keys are strings, and the viewer indexes by the same id it
// already uses for window frames).
type noteStore struct {
	mu    sync.RWMutex
	byWin map[string][]windowNote
	// pending holds what viewers did, until `reminal mcp` drains it. Without
	// this the MCP never learns about a dismissal made from the web and its
	// next publish resurrects the note.
	pending []noteAct
}

// noteAct is one viewer action awaiting collection by the MCP process.
type noteAct struct {
	Window string `json:"window"`
	ID     string `json:"id"`
	Action string `json:"action"`
}

func newNoteStore() *noteStore { return &noteStore{byWin: map[string][]windowNote{}} }

// replace swaps the whole picture. `reminal mcp` publishes complete state rather
// than deltas: it is small, and a dropped delta would leave a viewer showing a
// note the badge no longer has, with nothing to correct it.
func (n *noteStore) replace(next map[string][]windowNote) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.byWin = map[string][]windowNote{}
	for w, list := range next {
		if len(list) > 0 {
			n.byWin[w] = list
		}
	}
}

func (n *noteStore) snapshot() map[string][]windowNote {
	n.mu.RLock()
	defer n.mu.RUnlock()
	out := make(map[string][]windowNote, len(n.byWin))
	for w, list := range n.byWin {
		out[w] = list
	}
	return out
}

func (n *noteStore) empty() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.byWin) == 0
}

// notesPayload is the wire shape for TypeWindowNotes.
type notesPayload struct {
	Notes map[string][]windowNote `json:"notes"`
}

// broadcastNotes pushes the current notes to every viewer. Called when
// `reminal mcp` publishes a change, and again when a viewer connects so a tab
// opened after the fact isn't blank until something else moves.
func (a *Agent) broadcastNotes() error {
	if a.notes == nil {
		return nil
	}
	payload, err := json.Marshal(notesPayload{Notes: a.notes.snapshot()})
	if err != nil {
		return err
	}
	enc, err := a.box.Encrypt(payload)
	if err != nil {
		return err
	}
	a.currentConnMu.Lock()
	conn := a.currentConn
	a.currentConnMu.Unlock()
	if conn == nil {
		return nil // not connected; the next viewer connect re-sends
	}
	return a.writeMsg(conn, protocol.Message{Type: protocol.TypeWindowNotes, Data: enc})
}

// setNotesJSON accepts the JSON published over the control socket and pushes the
// result to viewers.
func (a *Agent) setNotesJSON(raw string) error {
	var in notesPayload
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return err
	}
	if a.notes == nil {
		a.notes = newNoteStore()
	}
	a.notes.replace(in.Notes)
	return a.broadcastNotes()
}

// handleNoteAct applies a viewer's Done / Dismiss / Dismiss-all, mirroring what
// the desktop badge's own buttons do, then re-broadcasts so every viewer agrees.
//
// Handback flips the note to `handback` rather than deleting it: that state is
// the whole point of the loop — it tells the agent the user did their part and
// it is its turn again.
//
// Caveat worth knowing: the badge on screen is driven by `reminal mcp`, a
// different process, so acting here does not yet move the badge. Closing that
// gap needs the daemon to own the store rather than the MCP process.
func (a *Agent) handleNoteAct(msg protocol.Message) {
	if a.notes == nil || len(msg.Data) == 0 {
		return
	}
	raw, err := a.box.Decrypt(msg.Data)
	if err != nil {
		return
	}
	var act struct {
		Window string `json:"window"`
		ID     string `json:"id"`
		Action string `json:"action"`
	}
	if err := json.Unmarshal(raw, &act); err != nil {
		return
	}

	a.notes.mu.Lock()
	list := a.notes.byWin[act.Window]
	switch act.Action {
	case "dismiss_all":
		delete(a.notes.byWin, act.Window)
	case "dismiss":
		kept := list[:0]
		for _, n := range list {
			if n.ID != act.ID {
				kept = append(kept, n)
			}
		}
		if len(kept) == 0 {
			delete(a.notes.byWin, act.Window)
		} else {
			a.notes.byWin[act.Window] = kept
		}
	case "handback":
		for i := range list {
			if list[i].ID == act.ID {
				list[i].Status = "handback"
				list[i].TS = time.Now().Unix()
			}
		}
		a.notes.byWin[act.Window] = list
	}
	a.notes.pending = append(a.notes.pending, noteAct{Window: act.Window, ID: act.ID, Action: act.Action})
	if len(a.notes.pending) > 200 {
		a.notes.pending = a.notes.pending[len(a.notes.pending)-200:]
	}
	a.notes.mu.Unlock()
	_ = a.broadcastNotes()
}

// takeNoteActs returns and clears what viewers have done since the last call.
// `reminal mcp` polls this so a dismissal made on the web reaches the badge's
// owner instead of being undone by the next publish.
func (a *Agent) takeNoteActs() string {
	if a.notes == nil {
		return "[]"
	}
	a.notes.mu.Lock()
	acts := a.notes.pending
	a.notes.pending = nil
	a.notes.mu.Unlock()
	if len(acts) == 0 {
		return "[]"
	}
	b, err := json.Marshal(acts)
	if err != nil {
		return "[]"
	}
	return string(b)
}
