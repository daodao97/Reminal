// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package main

// `reminal mcp` — an MCP (Model Context Protocol) stdio server exposing the
// window-comment overlay as agent tools, so any MCP-capable agent can leave a
// note ON the window the note is about and learn when the user hands it back.
//
// Why it lives in the reminal binary rather than as a script: an MCP server is
// only useful if it is trivially registerable, and "point your agent at this
// python file in a repo" is not that. `reminal integrate` registers
// `<reminal> mcp`, which means one binary, no interpreter, and a path that
// survives upgrades.
//
// Transport is MCP stdio: newline-delimited JSON-RPC 2.0. stdout therefore
// carries protocol traffic ONLY — anything else corrupts the stream, so all
// diagnostics go to stderr.
//
// State is deliberately in-memory: notes live exactly as long as their window
// (a closed window's list is forgotten by design), so there is nothing to
// persist and the ephemeral CGWindowID is a perfectly good key.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const mcpProtocolFallback = "2024-11-05"

// maxOverlayWindows bounds how many windows can carry badges at once — each is a
// helper process. maxReplyEvents bounds the reply backlog for an agent that
// never calls read_replies.
const (
	maxOverlayWindows = 12
	maxReplyEvents    = 500
)

// mcpInstructions is handed to the client on initialize and is what the model
// actually reads. It matters as much as the schemas: the difference between a
// badge used well and a badge that becomes noise is almost entirely here.
const mcpInstructions = `reminal lets you leave notes attached to a specific window on the user's screen — a small floating badge on that window, rather than text buried in a terminal they may not be looking at.

Use it when what you want to say is ABOUT a particular window: the editor holding the file you changed, the browser showing the failing page, the app whose settings need fixing. Do not use it for ordinary conversation — answer in chat as normal.

Workflow:
  1. list_windows to find the window your note belongs to.
  2. add_note with a status:
       attention — you are BLOCKED and need the user to act. Red, and the only status that pulses. Use sparingly; this interrupts.
       working   — you are mid-task on this. Ambient progress, no action wanted.
       info      — worth seeing, no action needed.
       done      — finished, nothing owed.
  3. If you posted 'attention', call read_replies later. A 'handback' reply means the user pressed Done and it is your turn again — pick the work back up.

Notes are ephemeral: they live only as long as the window, and you will see a 'closed' reply when it goes away. Keep titles to a few words — the badge is a glance surface, and the body carries detail. Only three notes are visible before the list scrolls, so remove notes you no longer need instead of letting them pile up.`

// ---------------------------------------------------------------- overlay children

// overlayHelper is the single reminal-overlay process serving every badged
// window. It used to be one process per window at ~26MB each; multiplexing also
// collapses N identical window-list enumerations per tick down to one.
type overlayHelper struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
}

type mcpServer struct {
	mu       sync.Mutex
	seq      uint64 // disambiguates auto-generated note ids
	helper   *overlayHelper
	attached map[uint32]bool  // windows currently carrying a badge
	events   []map[string]any // replies from the user, oldest first
}

func newMCPServer() *mcpServer {
	return &mcpServer{attached: map[uint32]bool{}}
}

// overlayHelperPath mirrors how the agent finds reminal-capture: explicit
// override, then alongside this binary (the release layout), then PATH.
func overlayHelperPath() (string, error) {
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

func (s *mcpServer) ensureHelper() (*overlayHelper, error) {
	s.mu.Lock()
	if s.helper != nil && s.helper.cmd.ProcessState == nil {
		h := s.helper
		s.mu.Unlock()
		return h, nil
	}
	s.mu.Unlock()

	bin, err := overlayHelperPath()
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
	h := &overlayHelper{cmd: cmd, stdin: stdin}
	s.mu.Lock()
	s.helper = h
	s.attached = map[uint32]bool{} // a fresh helper carries no badges
	s.mu.Unlock()
	go s.readReplies(stdout)
	return h, nil
}

// readReplies collects what the user did — handback / dismiss / closed — from
// the one helper, for every window it serves.
func (s *mcpServer) readReplies(r io.ReadCloser) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var ev map[string]any
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		ev["received_at"] = time.Now().Unix()
		s.mu.Lock()
		s.events = append(s.events, ev)
		if len(s.events) > maxReplyEvents {
			s.events = s.events[len(s.events)-maxReplyEvents:]
		}
		// Window gone, list forgotten: free its slot, but the helper lives on
		// serving the others.
		if ev["event"] == "closed" {
			if w, ok := ev["window"].(float64); ok {
				delete(s.attached, uint32(w))
			}
		}
		s.mu.Unlock()
	}
	s.mu.Lock()
	s.helper = nil
	s.attached = map[uint32]bool{}
	s.mu.Unlock()
}

// send delivers one command for one window, attaching the badge first if needed.
func (s *mcpServer) send(window uint32, payload map[string]any) error {
	h, err := s.ensureHelper()
	if err != nil {
		return err
	}
	s.mu.Lock()
	needAttach := !s.attached[window]
	if needAttach && len(s.attached) >= maxOverlayWindows {
		s.mu.Unlock()
		return fmt.Errorf("already showing notes on %d windows (the limit); "+
			"clear one with clear_notes before annotating another", maxOverlayWindows)
	}
	if needAttach {
		s.attached[window] = true
	}
	s.mu.Unlock()

	write := func(m map[string]any) error {
		b, _ := json.Marshal(m)
		if _, err := h.stdin.Write(append(b, '\n')); err != nil {
			s.mu.Lock()
			s.helper = nil
			s.attached = map[uint32]bool{}
			s.mu.Unlock()
			return errors.New("the overlay helper went away")
		}
		return nil
	}
	if needAttach {
		if err := write(map[string]any{
			"cmd": "attach", "window": window, "corner": "tr", "placement": "float",
		}); err != nil {
			return err
		}
	}
	payload["window"] = window
	return write(payload)
}

// detach drops one window's badge and frees its slot, leaving the rest running.
func (s *mcpServer) detach(window uint32) {
	s.mu.Lock()
	delete(s.attached, window)
	s.mu.Unlock()
}

// shutdown stops every badge. MCP clients restart their servers freely, and an
// orphaned overlay would leave stale badges stuck on windows with nothing able
// to clear them.
func (s *mcpServer) shutdown() {
	s.mu.Lock()
	h := s.helper
	s.helper = nil
	s.attached = map[uint32]bool{}
	s.mu.Unlock()
	if h == nil {
		return
	}
	_, _ = h.stdin.Write([]byte("{\"cmd\":\"quit\"}\n"))
	done := make(chan struct{})
	go func() { _ = h.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		if h.cmd.Process != nil {
			_ = h.cmd.Process.Kill()
		}
	}
}

// ---------------------------------------------------------------- tools

func mcpToolList() []map[string]any {
	obj := func(props map[string]any, required ...string) map[string]any {
		m := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			m["required"] = required
		}
		return m
	}
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	return []map[string]any{
		{
			"name": "list_windows",
			"description": "List the windows open on the user's screen with the window_id the other tools need. " +
				"Call this first — window ids are ephemeral and change when an app restarts.",
			"inputSchema": obj(map[string]any{}),
		},
		{
			"name": "add_note",
			"description": "Attach a note to a window, shown as a small floating badge on that window. " +
				"Reusing an existing note_id updates that note in place — use that to move a note from " +
				"'working' to 'done' rather than adding a second one.",
			"inputSchema": obj(map[string]any{
				"window_id": map[string]any{"type": "integer", "description": "From list_windows."},
				"title":     str("Short headline, a few words. Shown in bold."),
				"body":      str("Optional detail, one or two sentences."),
				"status": map[string]any{
					"type": "string", "enum": []string{"attention", "working", "info", "done"},
					"default": "info",
					"description": "attention = you are blocked and need the user (red, pulses, interrupts); " +
						"working = in progress; info = FYI; done = finished.",
				},
				"note_id": str("Stable id for this note; pass it again to update it in place."),
				"author":  str("Who is speaking, e.g. your agent name."),
			}, "window_id", "title"),
		},
		{
			"name":        "remove_note",
			"description": "Remove one note from a window's list.",
			"inputSchema": obj(map[string]any{
				"window_id": map[string]any{"type": "integer"},
				"note_id":   str("Id given to add_note."),
			}, "window_id", "note_id"),
		},
		{
			"name":        "clear_notes",
			"description": "Remove every note from a window and take the badge off it.",
			"inputSchema": obj(map[string]any{
				"window_id": map[string]any{"type": "integer"},
			}, "window_id"),
		},
		{
			"name": "read_replies",
			"description": "Read what the user did with your notes since the last call. 'handback' means they " +
				"pressed Done and it is your turn again; 'dismiss' means they cleared it; 'closed' means the " +
				"window went away and its list is gone. Replies are returned once, then forgotten.",
			"inputSchema": obj(map[string]any{
				"window_id": map[string]any{"type": "integer", "description": "Optional: only this window's replies."},
			}),
		},
	}
}

func argInt(args map[string]any, key string) (uint32, error) {
	switch v := args[key].(type) {
	case float64:
		return uint32(v), nil
	case string:
		n, err := strconv.ParseUint(v, 10, 32)
		return uint32(n), err
	}
	return 0, fmt.Errorf("%s is required", key)
}

func argStr(args map[string]any, key, def string) string {
	if s, ok := args[key].(string); ok && s != "" {
		return s
	}
	return def
}

func (s *mcpServer) callTool(name string, args map[string]any) (string, error) {
	if runtime.GOOS != "darwin" && name != "read_replies" {
		return "", errors.New("window notes are macOS-only for now")
	}
	switch name {
	case "list_windows":
		bin, err := overlayHelperPath()
		if err != nil {
			return "", err
		}
		out, err := exec.Command(bin, "windows").Output()
		if err != nil {
			return "", fmt.Errorf("listing windows: %w", err)
		}
		if len(strings.TrimSpace(string(out))) == 0 {
			return "No windows found.", nil
		}
		return "window_id\tapp\ttitle\tbounds\n" + strings.TrimRight(string(out), "\n"), nil

	case "add_note":
		wid, err := argInt(args, "window_id")
		if err != nil {
			return "", err
		}
		title := argStr(args, "title", "")
		if title == "" {
			return "", errors.New("title is required")
		}
		// A millisecond stamp alone collides: two add_note calls in the same
		// millisecond produced the same id, and on one window the second would
		// silently overwrite the first instead of adding a note.
		s.mu.Lock()
		s.seq++
		seq := s.seq
		s.mu.Unlock()
		id := argStr(args, "note_id", fmt.Sprintf("n%d-%d", time.Now().UnixMilli(), seq))
		err = s.send(wid, map[string]any{
			"cmd": "upsert", "id": id,
			"status": argStr(args, "status", "info"),
			"title":  title, "body": argStr(args, "body", ""),
			"author": argStr(args, "author", "agent"),
		})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Note %s posted to window %d.", id, wid), nil

	case "remove_note":
		wid, err := argInt(args, "window_id")
		if err != nil {
			return "", err
		}
		id := argStr(args, "note_id", "")
		if id == "" {
			return "", errors.New("note_id is required")
		}
		if err := s.send(wid, map[string]any{"cmd": "remove", "id": id}); err != nil {
			return "", err
		}
		return "Removed " + id + ".", nil

	case "clear_notes":
		wid, err := argInt(args, "window_id")
		if err != nil {
			return "", err
		}
		if err := s.send(wid, map[string]any{"cmd": "clear"}); err != nil {
			return "", err
		}
		// Also drop the panel, so the window-count limit reflects reality.
		_ = s.send(wid, map[string]any{"cmd": "detach"})
		s.detach(wid)
		return "Cleared.", nil

	case "read_replies":
		want, hasWant := args["window_id"].(float64)
		s.mu.Lock()
		var out, keep []map[string]any
		for _, ev := range s.events {
			w, _ := ev["window"].(float64)
			if !hasWant || w == want {
				out = append(out, ev)
			} else {
				keep = append(keep, ev)
			}
		}
		s.events = keep
		s.mu.Unlock()
		if len(out) == 0 {
			return "No replies.", nil
		}
		var b strings.Builder
		for _, ev := range out {
			line, _ := json.Marshal(ev)
			b.Write(line)
			b.WriteByte('\n')
		}
		return strings.TrimRight(b.String(), "\n"), nil
	}
	return "", fmt.Errorf("unknown tool: %s", name)
}

// ---------------------------------------------------------------- JSON-RPC

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func runMCP(_ []string) error {
	srv := newMCPServer()
	defer srv.shutdown()

	out := json.NewEncoder(os.Stdout)
	reply := func(id json.RawMessage, result any) {
		_ = out.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var msg rpcMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		switch msg.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			// Named `proto`, not `version`: the latter shadows reminal's own
			// build version and silently reports the protocol string as the
			// server version.
			proto := p.ProtocolVersion
			if proto == "" {
				proto = mcpProtocolFallback
			}
			reply(msg.ID, map[string]any{
				"protocolVersion": proto,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "reminal", "version": version},
				"instructions":    mcpInstructions,
			})
		case "ping":
			reply(msg.ID, map[string]any{})
		case "tools/list":
			reply(msg.ID, map[string]any{"tools": mcpToolList()})
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			text, err := srv.callTool(p.Name, p.Arguments)
			if err != nil {
				// Tool failures are results, not protocol errors — the model
				// should see them and be able to correct course.
				reply(msg.ID, map[string]any{
					"content": []any{map[string]any{"type": "text", "text": "Error: " + err.Error()}},
					"isError": true,
				})
				continue
			}
			reply(msg.ID, map[string]any{
				"content": []any{map[string]any{"type": "text", "text": text}},
			})
		default:
			if len(msg.ID) == 0 {
				continue // a notification; nothing to answer
			}
			_ = out.Encode(map[string]any{
				"jsonrpc": "2.0", "id": msg.ID,
				"error": map[string]any{"code": -32601, "message": "method not found: " + msg.Method},
			})
		}
	}
	return nil
}
