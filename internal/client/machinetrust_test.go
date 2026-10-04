// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"reminal/internal/crypto"
	"reminal/internal/protocol"
)

// answeringMachine answers an owner handshake as a machine holding the given
// key, completing it correctly.
func answeringMachine(t *testing.T, sessionID string, machine ed25519.PrivateKey) string {
	t.Helper()
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_, raw, err := c.ReadMessage()
		if err != nil {
			return
		}
		var in protocol.Message
		if json.Unmarshal(raw, &in) != nil || in.Type != protocol.TypeOwnerInit {
			return
		}
		vEph, _ := base64.StdEncoding.DecodeString(in.Data)
		devPub, _ := base64.StdEncoding.DecodeString(in.DevicePub)
		exID, _ := hex.DecodeString(in.ExID)
		eph, _ := crypto.NewEphemeralKey()
		aEph := eph.PublicKey().Bytes()
		peer, _ := crypto.PeerPublicKey(vEph)
		shared, _ := eph.ECDH(peer)
		key := make([]byte, 32)
		_, _ = rand.Read(key)
		wrapped, _ := crypto.WrapSessionKey(shared, exID, key)
		machinePub := machine.Public().(ed25519.PublicKey)
		sig := crypto.SignOwner(machine, crypto.OwnerServerTranscript(sessionID, vEph, aEph, devPub, machinePub))
		_ = c.WriteJSON(protocol.Message{
			Type:       protocol.TypeOwnerResp,
			ExID:       in.ExID,
			Data:       base64.StdEncoding.EncodeToString(aEph),
			MachinePub: base64.StdEncoding.EncodeToString(machinePub),
			MachineSig: base64.StdEncoding.EncodeToString(sig),
			Wrap:       base64.StdEncoding.EncodeToString(wrapped),
		})
		_, _, _ = c.ReadMessage() // hold the socket open until the viewer is done
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

// ownerHandshake runs the real viewer side of the owner handshake against url.
// answer is what the person types if asked; "" means nobody is there to ask.
func ownerHandshake(t *testing.T, url, sessionID, answer string) (asked bool, err error) {
	t.Helper()
	conn, _, derr := websocket.DefaultDialer.Dial(url, nil)
	if derr != nil {
		t.Fatal(derr)
	}
	defer conn.Close()
	v := &Viewer{sessionID: sessionID, owner: true}
	if answer != "" {
		in := make(chan []byte, 1)
		in <- []byte(answer)
		v.promptIn = in
		v.promptEsc = make(chan struct{})
		defer func() { asked = len(in) == 0 }()
	}
	return false, v.negotiateSessionKeyOwner(conn)
}

func machineKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

// stubSessionHome says which machine the directory would report a session on.
func stubSessionHome(t *testing.T, homes map[string]ed25519.PublicKey) {
	t.Helper()
	orig := findSessionHome
	findSessionHome = func(id string, _ []OwnedMachine) (ed25519.PublicKey, bool) {
		k, ok := homes[id]
		return k, ok
	}
	t.Cleanup(func() { findSessionHome = orig })
}

// A machine this device has connected to before is recognised on a session it
// has never opened: trust follows the machine, so there is nothing to ask.
func TestOwnerConnectKnownMachineNewSession(t *testing.T) {
	isolateHome(t)
	pub, priv := machineKeyPair(t)
	if err := RecordOwnedMachine(pub); err != nil {
		t.Fatal(err)
	}
	stubSessionHome(t, nil)
	asked, err := ownerHandshake(t, answeringMachine(t, "NEWSESS1", priv), "NEWSESS1", "n")
	if err != nil {
		t.Fatalf("known machine on a new session was refused: %v", err)
	}
	if asked {
		t.Fatal("asked about a machine this device already knows")
	}
}

// A session that one of this device's machines reports, answered by a
// different key, is refused without asking.
func TestOwnerConnectRefusesOtherKeyForOwnedMachinesSession(t *testing.T) {
	isolateHome(t)
	home, _ := machineKeyPair(t)
	if err := RecordOwnedMachine(home); err != nil {
		t.Fatal(err)
	}
	stubSessionHome(t, map[string]ed25519.PublicKey{"NEWSESS2": home})
	_, other := machineKeyPair(t)
	asked, err := ownerHandshake(t, answeringMachine(t, "NEWSESS2", other), "NEWSESS2", "y")
	if err == nil || !strings.Contains(err.Error(), "a different machine answered") {
		t.Fatalf("a different key on an owned machine's session got: %v", err)
	}
	if asked {
		t.Fatal("asked instead of refusing")
	}
	if _, ok, _ := PinnedMachineKey("NEWSESS2"); ok {
		t.Fatal("refused key was remembered for the session")
	}
}

// A machine the device has never connected to, on a session none of its
// machines report: connecting needs a yes, and a yes is remembered.
func TestOwnerConnectNewMachineNeedsConfirmation(t *testing.T) {
	isolateHome(t)
	stubSessionHome(t, nil)
	pub, priv := machineKeyPair(t)
	url := answeringMachine(t, "NEWSESS3", priv)

	if _, err := ownerHandshake(t, url, "NEWSESS3", ""); err != errMachineNotConfirmed {
		t.Fatalf("with nobody to ask: %v, want errMachineNotConfirmed", err)
	}
	if asked, err := ownerHandshake(t, answeringMachine(t, "NEWSESS3", priv), "NEWSESS3", "n"); err != errMachineNotConfirmed || !asked {
		t.Fatalf("answered n: asked=%v err=%v", asked, err)
	}
	if asked, err := ownerHandshake(t, answeringMachine(t, "NEWSESS3", priv), "NEWSESS3", "y"); err != nil || !asked {
		t.Fatalf("answered y: asked=%v err=%v", asked, err)
	}
	got, ok, _ := PinnedMachineKey("NEWSESS3")
	if !ok || !got.Equal(pub) {
		t.Fatal("confirmed machine was not remembered")
	}
}

// A session already pinned to a machine still refuses any other key.
func TestOwnerConnectPinnedSessionRefusesOtherKey(t *testing.T) {
	isolateHome(t)
	stubSessionHome(t, nil)
	pub, _ := machineKeyPair(t)
	if _, err := RecordMachineKey("OLDSESS1", pub); err != nil {
		t.Fatal(err)
	}
	_, other := machineKeyPair(t)
	if _, err := ownerHandshake(t, answeringMachine(t, "OLDSESS1", other), "OLDSESS1", "y"); err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("pinned session answered by another key: %v", err)
	}
}
