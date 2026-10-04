// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"reminal/internal/crypto"
	"reminal/internal/protocol"
)

// pakeAgent is a session agent holding a PIN and a session key to hand out.
func pakeAgent(t *testing.T) *Agent {
	t.Helper()
	key, err := crypto.NewSessionKey()
	if err != nil {
		t.Fatal(err)
	}
	return &Agent{sessionID: "PAKETEST", pin: "246810", sessionKey: key}
}

// viewerHalf runs the viewer's side of the handshake against the real agent
// handler and returns the session key it unwraps, or an error.
func viewerHalf(t *testing.T, a *Agent, pin string) ([]byte, bool) {
	t.Helper()
	conn, got := ownerConn(t)
	exHex, exID, err := crypto.NewExID()
	if err != nil {
		t.Fatal(err)
	}
	st, mine, err := crypto.NewCPace([]byte(pin), []byte(pakeChannel), pakeSID(a.sessionID, exID), true)
	if err != nil {
		t.Fatal(err)
	}
	a.handlePakeInit(conn, exHex, base64.StdEncoding.EncodeToString(mine), 0, "")
	reply, ok := nextOwnerMsg(t, got)
	if !ok {
		t.Fatal("the agent did not answer the handshake")
	}
	if reply.Type != protocol.TypePakeResp || reply.ExID != exHex {
		t.Fatalf("reply = %+v, want pake_resp for this exchange", reply)
	}
	agentElem, _ := base64.StdEncoding.DecodeString(reply.Data)
	k, err := st.Finish(agentElem)
	if err != nil {
		return nil, false
	}
	wrapped, _ := base64.StdEncoding.DecodeString(reply.Wrap)
	sk, err := crypto.UnwrapSessionKey(k, exID, wrapped)
	if err != nil {
		return nil, false
	}
	return sk, true
}

func TestPakeRightPINGetsTheSessionKey(t *testing.T) {
	a := pakeAgent(t)
	sk, ok := viewerHalf(t, a, "246810")
	if !ok {
		t.Fatal("the right PIN could not unwrap the session key")
	}
	if !bytes.Equal(sk, a.sessionKey) {
		t.Fatal("unwrapped a key, but not the session key")
	}
}

func TestPakeWrongPINGetsNothingUsable(t *testing.T) {
	a := pakeAgent(t)
	if _, ok := viewerHalf(t, a, "246811"); ok {
		t.Fatal("a wrong PIN unwrapped the session key")
	}
}

// The agent must not answer the handshake it replaced. This drives the agent's
// real read loop: the far end sends kex_init, and any reply at all means the old
// exchange is still being served.
func TestAgentNoLongerAnswersKexInit(t *testing.T) {
	a := pakeAgent(t)
	replies := make(chan protocol.Message, 8)
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		exHex, _, _ := crypto.NewExID()
		_ = c.WriteJSON(protocol.Message{
			Type: protocol.TypeKexInit,
			ExID: exHex,
			Data: base64.StdEncoding.EncodeToString(make([]byte, 32)),
		})
		// A pake_init on the same socket proves the loop is reading and
		// answering at all, so silence to kex_init means something.
		ex2, exID2, _ := crypto.NewExID()
		_, mine, _ := crypto.NewCPace([]byte(a.pin), []byte(pakeChannel), pakeSID(a.sessionID, exID2), true)
		_ = c.WriteJSON(protocol.Message{
			Type: protocol.TypePakeInit,
			ExID: ex2,
			Data: base64.StdEncoding.EncodeToString(mine),
		})
		for {
			var m protocol.Message
			if err := c.ReadJSON(&m); err != nil {
				return
			}
			replies <- m
		}
	}))
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	go func() { _ = a.runReader(conn, make(chan uint64, 8)) }()

	deadline := time.After(3 * time.Second)
	sawPake := false
	for !sawPake {
		select {
		case m := <-replies:
			switch m.Type {
			case protocol.TypeKexResp:
				t.Fatalf("the agent answered kex_init: %+v", m)
			case protocol.TypePakeResp:
				sawPake = true
			}
		case <-deadline:
			t.Fatal("the agent answered neither handshake — the read loop is not running, so this test proves nothing")
		}
	}
	// Anything still in flight for kex_init would have arrived before the
	// pake reply, since both were sent in that order on one socket.
	select {
	case m := <-replies:
		if m.Type == protocol.TypeKexResp {
			t.Fatalf("the agent answered kex_init: %+v", m)
		}
	case <-time.After(300 * time.Millisecond):
	}
}
