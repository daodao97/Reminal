// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package relay

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"reminal/internal/protocol"
)

// agentAuth connects as the agent of sessionID and reports what the relay
// answered: "" for auth_ok, the error otherwise. The socket is closed after.
func agentAuth(t *testing.T, wsURL, sessionID, token string) string {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial(wsURL+"/"+sessionID+"/agent", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	if err := c.WriteJSON(protocol.Message{Type: protocol.TypeAuth, Token: token}); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var m protocol.Message
	if err := c.ReadJSON(&m); err != nil {
		return "closed: " + err.Error()
	}
	if m.Type == protocol.TypeAuthOK {
		return ""
	}
	return m.Error
}

// A session ID whose agent went away stays its agent's: once the room is
// cleared, a different credential is refused and the original one is not,
// until the reservation runs out.
func TestClearedRoomStaysWithItsAgent(t *testing.T) {
	s := NewServer()
	s.orphanWait, s.ownerWait = 50*time.Millisecond, 600*time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		s.HandleSessionWS(w, r, parts[0], parts[1])
	}))
	defer srv.Close()
	ws := "ws" + strings.TrimPrefix(srv.URL, "http")

	const id = "OWNEDSES"
	if e := agentAuth(t, ws, id, "REAL-TOKEN"); e != "" {
		t.Fatalf("first agent: %s", e)
	}
	// Disconnected above; let the room be cleared.
	time.Sleep(200 * time.Millisecond)
	if s.getRoom(id) != nil {
		t.Fatal("room was not cleared")
	}
	if e := agentAuth(t, ws, id, "SOMEONE-ELSE"); e == "" {
		t.Fatal("another credential claimed a cleared session ID")
	}
	time.Sleep(100 * time.Millisecond) // the failed claim's room clears too
	if e := agentAuth(t, ws, id, "REAL-TOKEN"); e != "" {
		t.Fatalf("the session's own agent was refused: %s", e)
	}
	// Once the reservation has run out, the ID is free again.
	time.Sleep(s.ownerWait + 300*time.Millisecond)
	if e := agentAuth(t, ws, id, "SOMEONE-ELSE"); e != "" {
		t.Fatalf("after the reservation ran out: %s", e)
	}
}

// A connection in the agent's place that never authenticates does not keep
// the agent out: the agent connects, authenticates and takes the slot, and
// the other connection is told it was superseded.
func TestUnauthenticatedAgentConnectionDoesNotHoldTheSlot(t *testing.T) {
	s := NewServer()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		s.HandleSessionWS(w, r, parts[0], parts[1])
	}))
	defer srv.Close()
	ws := "ws" + strings.TrimPrefix(srv.URL, "http")
	const id = "SLOTSESS"

	// The real agent registers first, so the room has a credential.
	if e := agentAuth(t, ws, id, "REAL-TOKEN"); e != "" {
		t.Fatalf("first agent: %s", e)
	}
	// Ten connections that never authenticate, held open.
	for i := 0; i < 10; i++ {
		c, _, err := websocket.DefaultDialer.Dial(ws+"/"+id+"/agent", nil)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
	}
	// The real agent must still get in, at once.
	c, _, err := websocket.DefaultDialer.Dial(ws+"/"+id+"/agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.WriteJSON(protocol.Message{Type: protocol.TypeAuth, Token: "REAL-TOKEN"})
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var m protocol.Message
	if err := c.ReadJSON(&m); err != nil || m.Type != protocol.TypeAuthOK {
		t.Fatalf("real agent with idle connections about: %v %s %s", err, m.Type, m.Error)
	}
	// And a wrong credential still gets nothing.
	if e := agentAuth(t, ws, id, "WRONG"); e == "" {
		t.Fatal("a wrong credential took the slot")
	}
}

// The legacy /ws path no longer takes agents at all, and a connection waiting
// to authenticate as the agent is not the agent: nothing it sends reaches the
// viewers.
func TestOnlyTheAuthenticatedAgentSpeaksForTheAgent(t *testing.T) {
	s := NewServer()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws" {
			s.HandleWS(w, r)
			return
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		s.HandleSessionWS(w, r, parts[0], parts[1])
	}))
	defer srv.Close()
	ws := "ws" + strings.TrimPrefix(srv.URL, "http")
	const id = "SPEAKSES"

	// The real agent, authenticated and kept open.
	agent, _, err := websocket.DefaultDialer.Dial(ws+"/"+id+"/agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	_ = agent.WriteJSON(protocol.Message{Type: protocol.TypeAuth, Token: "REAL"})
	_ = agent.SetReadDeadline(time.Now().Add(3 * time.Second))
	var m protocol.Message
	if err := agent.ReadJSON(&m); err != nil || m.Type != protocol.TypeAuthOK {
		t.Fatalf("agent auth: %v %s", err, m.Type)
	}
	// A viewer, authenticated.
	viewer, _, err := websocket.DefaultDialer.Dial(ws+"/"+id+"/viewer", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.Close()
	_ = viewer.WriteJSON(protocol.Message{Type: protocol.TypeAuth})
	_ = viewer.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err := viewer.ReadJSON(&m); err != nil || m.Type != protocol.TypeAuthOK {
		t.Fatalf("viewer auth: %v %s", err, m.Type)
	}
	// Read until the agent's first message arrives: whatever presence
	// notices come first are not the point.
	waitData := func(want string) (seen []protocol.MessageType) {
		for {
			_ = viewer.SetReadDeadline(time.Now().Add(3 * time.Second))
			var got protocol.Message
			if err := viewer.ReadJSON(&got); err != nil {
				t.Fatalf("waiting for %q: %v (seen %v)", want, err, seen)
			}
			if got.Type == protocol.TypeData {
				if got.Data != want {
					t.Fatalf("data %q before %q (seen %v)", got.Data, want, seen)
				}
				return seen
			}
			seen = append(seen, got.Type)
		}
	}
	_ = agent.WriteJSON(protocol.Message{Type: protocol.TypeData, Data: "first", Seq: 1})
	waitData("first")

	// Legacy path: a register is refused outright.
	legacy, _, err := websocket.DefaultDialer.Dial(ws+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	_ = legacy.WriteJSON(protocol.Message{Type: protocol.TypeRegister, SessionID: id})
	_ = legacy.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err := legacy.ReadJSON(&m); err != nil || m.Type != protocol.TypeError {
		t.Fatalf("legacy register: %v %s %q", err, m.Type, m.Error)
	}

	// A pending (never authenticated) agent connection sends session messages,
	// then the real agent sends one: the viewer must see only the latter.
	pending, _, err := websocket.DefaultDialer.Dial(ws+"/"+id+"/agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer pending.Close()
	_ = pending.WriteJSON(protocol.Message{Type: protocol.TypeOwnerBusy, ExID: "deadbeef", RetryMS: 1000})
	time.Sleep(300 * time.Millisecond)
	_ = agent.WriteJSON(protocol.Message{Type: protocol.TypeData, Data: "second", Seq: 2})
	for _, typ := range waitData("second") {
		if typ == protocol.TypeOwnerBusy {
			t.Fatal("viewer received own_busy from a connection that never authenticated")
		}
	}
}
