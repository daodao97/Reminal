// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

// The daemon's window-notes service: the single owner of what each window is
// showing, and of the one reminal-overlay process that draws the badges.
//
// Why it lives here rather than in `reminal mcp`, where it started: every coding
// agent with reminal registered spawns its OWN `reminal mcp`. Four of them
// running for days is an ordinary state, not a leak. When each kept a private
// copy of the notes and published the complete set to the session agents, they
// silently fought:
//
//   - a note dismissed from a phone came back, because the dismissal reached
//     whichever publisher polled first and the others carried it forever;
//   - one publisher's publish erased every other publisher's windows, since a
//     publish replaces the whole picture;
//   - the badge on screen belonged to whichever process attached first, so it
//     and the viewer could disagree indefinitely;
//   - and an MCP client restarting its server (`claude mcp list` does exactly
//     that) threw that server's notes away.
//
// None of those are fixable by being careful in the publishers, because the
// publishers are the problem: N owners of one truth. The daemon is already the
// machine's always-on singleton — install.sh starts it regardless of ownership —
// so it owns the notes, owns the badge, and every `reminal mcp` becomes a thin
// stateless client. The session agents keep a read-only mirror purely so viewers
// have something to render.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// notesSockPath is the fixed socket the daemon serves notes on, so any
// `reminal mcp` can find it. REMINAL_NOTES_SOCK overrides it for the same
// reason mirror.sock has an override: so a development daemon can be run beside
// the installed one instead of seizing its socket.
func notesSockPath() (string, error) {
	if p := strings.TrimSpace(os.Getenv("REMINAL_NOTES_SOCK")); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".reminal", "notes.sock"), nil
}

const (
	// maxNotesPerWindow bounds one window's list the same way the badge does.
	maxNotesPerWindow = 50
	// maxNoteWindows bounds how many windows can carry badges at once.
	maxNoteWindows = 12
	// maxReplyLog bounds the reply ring the MCP clients read from.
	maxReplyLog = 500
)

// notesReq is one request on the notes socket (newline-delimited JSON).
type notesReq struct {
	Cmd    string      `json:"cmd"`
	Window uint32      `json:"window,omitempty"`
	ID     string      `json:"id,omitempty"`
	Note   *windowNote `json:"note,omitempty"`
	Action string      `json:"action,omitempty"`
	// Since is the reply sequence a client has already consumed. Replies are a
	// log rather than a queue precisely so several MCP clients can each read
	// every event — the drain-once queue this service replaces is what made
	// dismissals reach only one publisher.
	Since uint64 `json:"since,omitempty"`
}

type notesResp struct {
	OK  bool   `json:"ok"`
	Err string `json:"err,omitempty"`
	// Warn reports that the note was stored and mirrored but the on-screen
	// badge could not be drawn — a missing helper, or simply a platform that
	// has no badge yet. Non-fatal by design: the store is the product and the
	// badge is one rendering of it, so a host without one still shows notes on
	// the phone.
	Warn    string                  `json:"warn,omitempty"`
	Notes   map[string][]windowNote `json:"notes,omitempty"`
	Replies []map[string]any        `json:"replies,omitempty"`
	Seq     uint64                  `json:"seq,omitempty"`
}

// notesDaemon is the authoritative store.
type notesDaemon struct {
	mu       sync.Mutex
	notes    map[uint32][]windowNote
	attached map[uint32]bool
	helper   *overlayProc
	replies  []map[string]any
	replySeq uint64
}

// overlayProc is the single reminal-overlay process serving every badged
// window. One process per window cost ~26MB each; multiplexing also collapses N
// identical window-list enumerations per tick down to one.
type overlayProc struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
}

func newNotesDaemon() *notesDaemon {
	return &notesDaemon{
		notes:    map[uint32][]windowNote{},
		attached: map[uint32]bool{},
	}
}

// ServeNotes runs the daemon's notes service until stop closes. Started by
// RunDaemon on every platform: the badge itself is macOS-only today, but the
// store and the fan-out to viewers are not, so a Linux or Windows host still
// mirrors notes to phones.
func ServeNotes(stop <-chan struct{}) {
	serveNotesOn(newNotesDaemon(), stop)
}

// serveNotesOn takes the store explicitly rather than reaching for a package
// global. The service owns exactly one instance in production, but a global
// makes that instance impossible to swap and races with anything that replaces
// it — which the race detector caught immediately.
func serveNotesOn(d *notesDaemon, stop <-chan struct{}) {
	sock, err := notesSockPath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		return
	}
	_ = os.Remove(sock) // clear a stale socket left by a crash
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return
	}
	_ = os.Chmod(sock, 0o600)
	go func() {
		<-stop
		_ = ln.Close()
		_ = os.Remove(sock)
		d.shutdown()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-stop:
				return
			default:
			}
			// Any other Accept error (e.g. EMFILE under fd pressure) must not kill
			// the service — notes would then fail for every agent until the daemon
			// restarts. Back off briefly and keep accepting, as serveMirror does.
			time.Sleep(50 * time.Millisecond)
			continue
		}
		go d.handleConn(conn)
	}
}

func (d *notesDaemon) handleConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	enc := json.NewEncoder(conn)
	for sc.Scan() {
		var req notesReq
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			_ = enc.Encode(notesResp{Err: "bad request"})
			continue
		}
		_ = enc.Encode(d.dispatch(req))
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	}
}

func (d *notesDaemon) dispatch(req notesReq) notesResp {
	switch req.Cmd {
	case "ping":
		return notesResp{OK: true}
	case "get":
		return notesResp{OK: true, Notes: d.snapshot()}
	case "add":
		if req.Note == nil {
			return notesResp{Err: "missing note"}
		}
		warn, err := d.add(req.Window, *req.Note)
		if err != nil {
			return notesResp{Err: err.Error()}
		}
		return notesResp{OK: true, Warn: warn}
	case "remove":
		d.remove(req.Window, req.ID)
		return notesResp{OK: true}
	case "clear":
		d.clear(req.Window)
		return notesResp{OK: true}
	case "act":
		d.applyAct(req.Window, req.ID, req.Action)
		return notesResp{OK: true}
	case "replies":
		evs, seq := d.repliesSince(req.Since)
		return notesResp{OK: true, Replies: evs, Seq: seq}
	default:
		return notesResp{Err: "unknown command " + req.Cmd}
	}
}

func (d *notesDaemon) snapshot() map[string][]windowNote {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[string][]windowNote, len(d.notes))
	for w, list := range d.notes {
		if len(list) > 0 {
			out[strconv.FormatUint(uint64(w), 10)] = append([]windowNote(nil), list...)
		}
	}
	return out
}

// add upserts a note on a window, attaching a badge if this is the window's
// first. Upsert on id, because an agent re-raising the same note_id means
// "update this", not "add a duplicate".
func (d *notesDaemon) add(win uint32, n windowNote) (string, error) {
	d.mu.Lock()
	if _, live := d.notes[win]; !live && len(d.notes) >= maxNoteWindows {
		d.mu.Unlock()
		return "", fmt.Errorf("too many badged windows (%d)", maxNoteWindows)
	}
	list := d.notes[win]
	updated := false
	for i := range list {
		if list[i].ID == n.ID {
			list[i] = n
			updated = true
			break
		}
	}
	if !updated {
		list = append(list, n)
	}
	if len(list) > maxNotesPerWindow {
		list = list[len(list)-maxNotesPerWindow:]
	}
	d.notes[win] = list
	d.mu.Unlock()

	// Store and mirror first, then try to draw. Doing it the other way round
	// meant a host with no badge helper — every Linux and Windows machine, or a
	// broken install — lost the note entirely instead of just not drawing it.
	d.publish()

	warn := ""
	if err := d.attach(win); err != nil {
		return "badge not shown: " + err.Error(), nil
	}
	if err := d.send(win, map[string]any{
		"cmd": "upsert", "id": n.ID, "status": n.Status,
		"title": n.Title, "body": n.Body, "author": n.Author,
	}); err != nil {
		warn = "badge not updated: " + err.Error()
	}
	return warn, nil
}

func (d *notesDaemon) remove(win uint32, id string) {
	d.mu.Lock()
	if list, ok := d.notes[win]; ok {
		kept := list[:0]
		for _, n := range list {
			if n.ID != id {
				kept = append(kept, n)
			}
		}
		if len(kept) == 0 {
			delete(d.notes, win)
		} else {
			d.notes[win] = kept
		}
	}
	d.mu.Unlock()
	_ = d.send(win, map[string]any{"cmd": "remove", "id": id})
	d.publish()
}

func (d *notesDaemon) clear(win uint32) {
	d.mu.Lock()
	delete(d.notes, win)
	d.mu.Unlock()
	_ = d.send(win, map[string]any{"cmd": "clear"})
	d.publish()
}

// applyAct is a viewer's Done / Dismiss / Dismiss-all, arriving from a session
// agent. Because the daemon owns both the store and the badge, acting from a
// phone now moves the dot on screen — the direction that could never work while
// each publisher held its own copy.
func (d *notesDaemon) applyAct(win uint32, id, action string) {
	switch action {
	case "dismiss":
		d.remove(win, id)
	case "dismiss_all":
		d.clear(win)
	case "handback":
		d.mu.Lock()
		for i := range d.notes[win] {
			if d.notes[win][i].ID == id {
				d.notes[win][i].Status = "handback"
				d.notes[win][i].TS = time.Now().Unix()
			}
		}
		d.mu.Unlock()
		_ = d.send(win, map[string]any{"cmd": "upsert", "id": id, "status": "handback"})
		d.publish()
		d.recordReply(map[string]any{"event": "handback", "window": win, "id": id})
	}
}

func (d *notesDaemon) repliesSince(since uint64) ([]map[string]any, uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]map[string]any, 0, len(d.replies))
	for _, ev := range d.replies {
		if seq, _ := ev["seq"].(uint64); seq > since {
			out = append(out, ev)
		}
	}
	return out, d.replySeq
}

func (d *notesDaemon) recordReply(ev map[string]any) {
	d.mu.Lock()
	d.replySeq++
	ev["seq"] = d.replySeq
	ev["received_at"] = time.Now().Unix()
	d.replies = append(d.replies, ev)
	if len(d.replies) > maxReplyLog {
		d.replies = d.replies[len(d.replies)-maxReplyLog:]
	}
	d.mu.Unlock()
}

// publish hands the whole current picture to every reminal agent running as
// this user, which mirrors it to their viewers.
//
// Broadcast rather than addressed: notes are machine-scoped, and this process
// has no idea which session — if any — the user happens to be watching.
// Best-effort throughout; a dead socket from an exited session must never fail
// a tool call.
func (d *notesDaemon) publish() {
	body, err := json.Marshal(notesPayload{Notes: d.snapshot()})
	if err != nil {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	socks, _ := filepath.Glob(filepath.Join(home, ".reminal", "agent-*.sock"))
	for _, sock := range socks {
		conn, err := net.DialTimeout("unix", sock, 300*time.Millisecond)
		if err != nil {
			continue
		}
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		_, _ = fmt.Fprintf(conn, "notes %s\n", body)
		_ = conn.Close()
	}
}

// ---- badge helper ----------------------------------------------------------

// overlayBinPath mirrors how the agent finds reminal-capture: explicit
// override, then alongside this binary (the release layout), then PATH.
func overlayBinPath() (string, error) {
	if p := os.Getenv("REMINAL_OVERLAY_BIN"); p != "" {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), "reminal-overlay")
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return cand, nil
		}
	}
	if p, err := exec.LookPath("reminal-overlay"); err == nil {
		return p, nil
	}
	return "", errors.New("reminal-overlay helper not found next to reminal or on PATH")
}

func (d *notesDaemon) ensureHelper() (*overlayProc, error) {
	d.mu.Lock()
	if d.helper != nil && d.helper.cmd.ProcessState == nil {
		h := d.helper
		d.mu.Unlock()
		return h, nil
	}
	d.mu.Unlock()

	bin, err := overlayBinPath()
	if err != nil {
		return nil, err
	}
	// No argv: the helper's default mode is a stdin-driven multiplexer. It shows
	// nothing until a window is attached and a note arrives.
	cmd := exec.Command(bin)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	h := &overlayProc{cmd: cmd, stdin: stdin}
	d.mu.Lock()
	d.helper = h
	d.attached = map[uint32]bool{} // a fresh helper carries no badges
	d.mu.Unlock()
	go d.readBadgeEvents(stdout)
	return h, nil
}

func (d *notesDaemon) attach(win uint32) error {
	d.mu.Lock()
	already := d.attached[win]
	d.mu.Unlock()
	if already {
		return nil
	}
	if _, err := d.ensureHelper(); err != nil {
		return err
	}
	if err := d.writeHelper(map[string]any{
		"cmd": "attach", "window": win, "corner": "tr", "placement": "float",
	}); err != nil {
		return err
	}
	d.mu.Lock()
	d.attached[win] = true
	d.mu.Unlock()
	return nil
}

func (d *notesDaemon) send(win uint32, payload map[string]any) error {
	d.mu.Lock()
	attached := d.attached[win]
	d.mu.Unlock()
	if !attached {
		return nil // nothing on screen for this window; the store is still updated
	}
	payload["window"] = win
	return d.writeHelper(payload)
}

func (d *notesDaemon) writeHelper(payload map[string]any) error {
	h, err := d.ensureHelper()
	if err != nil {
		return err
	}
	line, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err = h.stdin.Write(append(line, '\n'))
	return err
}

// readBadgeEvents collects what the user did on screen — handback, dismiss, a
// window closing — and applies it to the store, so the badge and the phone stay
// the same picture.
func (d *notesDaemon) readBadgeEvents(r io.ReadCloser) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var ev map[string]any
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		w, hasWin := ev["window"].(float64)
		id, _ := ev["id"].(string)
		if hasWin {
			win := uint32(w)
			switch ev["event"] {
			case "closed":
				// A window that closes takes its notes with it — the badge is
				// gone and nothing could act on them any more.
				d.mu.Lock()
				delete(d.attached, win)
				delete(d.notes, win)
				d.mu.Unlock()
				d.publish()
			case "dismiss", "evicted":
				d.mu.Lock()
				if list, ok := d.notes[win]; ok {
					kept := list[:0]
					for _, n := range list {
						if n.ID != id {
							kept = append(kept, n)
						}
					}
					if len(kept) == 0 {
						delete(d.notes, win)
					} else {
						d.notes[win] = kept
					}
				}
				d.mu.Unlock()
				d.publish()
			case "handback":
				d.mu.Lock()
				for i := range d.notes[win] {
					if d.notes[win][i].ID == id {
						d.notes[win][i].Status = "handback"
					}
				}
				d.mu.Unlock()
				d.publish()
			}
		}
		d.recordReply(ev)
	}
}

func (d *notesDaemon) shutdown() {
	d.mu.Lock()
	h := d.helper
	d.helper = nil
	d.mu.Unlock()
	if h == nil {
		return
	}
	// Ask it to go away, then make sure. An orphaned helper leaves a stale badge
	// stuck on a window with nothing able to clear it.
	line, _ := json.Marshal(map[string]any{"cmd": "quit"})
	_, _ = h.stdin.Write(append(line, '\n'))
	_ = h.stdin.Close()
	done := make(chan struct{})
	go func() { _ = h.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		_ = h.cmd.Process.Kill()
	}
}
