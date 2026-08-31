// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

// Client side of the daemon's notes service, used by `reminal mcp` (which owns
// no state of its own) and by session agents forwarding what a viewer did.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

// NoteInput is one annotation as an MCP tool supplies it.
type NoteInput struct {
	ID     string
	Status string
	Title  string
	Body   string
	Author string
	TS     int64
}

// notesRoundTrip sends one request and reads one reply. A fresh connection per
// call: these are rare, tiny, and a long-lived connection would only add
// reconnect logic for no benefit.
func notesRoundTrip(req notesReq) (notesResp, error) {
	sock, err := notesSockPath()
	if err != nil {
		return notesResp{}, err
	}
	conn, err := net.DialTimeout("unix", sock, 500*time.Millisecond)
	if err != nil {
		return notesResp{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	body, err := json.Marshal(req)
	if err != nil {
		return notesResp{}, err
	}
	if _, err := fmt.Fprintf(conn, "%s\n", body); err != nil {
		return notesResp{}, err
	}
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	if !sc.Scan() {
		return notesResp{}, errors.New("no reply from notes service")
	}
	var resp notesResp
	if err := json.Unmarshal(sc.Bytes(), &resp); err != nil {
		return notesResp{}, err
	}
	if !resp.OK && resp.Err != "" {
		return resp, errors.New(resp.Err)
	}
	return resp, nil
}

// NotesDaemonReachable reports whether the daemon's notes service is answering.
// Callers fall back to holding notes in their own process when it isn't, so the
// feature degrades rather than disappearing on a machine with no daemon.
func NotesDaemonReachable() bool {
	_, err := notesRoundTrip(notesReq{Cmd: "ping"})
	return err == nil
}

// NotesAdd posts (or updates) a note on a window. The returned string is a
// non-fatal warning — the note is stored and mirrored, but the on-screen badge
// could not be drawn (no helper, or a platform without badges).
func NotesAdd(window uint32, in NoteInput) (string, error) {
	resp, err := notesRoundTrip(notesReq{Cmd: "add", Window: window, Note: &windowNote{
		ID: in.ID, Status: in.Status, Title: in.Title,
		Body: in.Body, Author: in.Author, TS: in.TS,
	}})
	return resp.Warn, err
}

func NotesRemove(window uint32, id string) error {
	_, err := notesRoundTrip(notesReq{Cmd: "remove", Window: window, ID: id})
	return err
}

func NotesClear(window uint32) error {
	_, err := notesRoundTrip(notesReq{Cmd: "clear", Window: window})
	return err
}

// NotesReplies returns badge events newer than `since`, plus the highest
// sequence now held. It is a log, not a queue: every MCP client reads every
// event and tracks its own cursor, so one client reading can't blind another —
// the failure that made dismissals reach only one publisher.
func NotesReplies(since uint64) ([]map[string]any, uint64, error) {
	resp, err := notesRoundTrip(notesReq{Cmd: "replies", Since: since})
	if err != nil {
		return nil, since, err
	}
	return resp.Replies, resp.Seq, nil
}

// notesForwardAct tells the daemon what a viewer did. Best-effort: the agent has
// already applied it to its own mirror, so a missing daemon costs the badge
// update, not the viewer's own view.
func notesForwardAct(window, id, action string) {
	var win uint64
	if _, err := fmt.Sscanf(window, "%d", &win); err != nil {
		return
	}
	_, _ = notesRoundTrip(notesReq{
		Cmd: "act", Window: uint32(win), ID: id, Action: action,
	})
}
