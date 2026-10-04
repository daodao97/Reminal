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
