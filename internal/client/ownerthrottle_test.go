// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"crypto/ed25519"
	"crypto/rand"
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

// ownerConn returns a socket the agent can answer on, plus everything it
// writes there.
func ownerConn(t *testing.T) (*websocket.Conn, <-chan protocol.Message) {
	t.Helper()
	got := make(chan protocol.Message, 256)
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			var m protocol.Message
			if err := c.ReadJSON(&m); err != nil {
				return
			}
			got <- m
		}
	}))
	t.Cleanup(srv.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn, got
}

// ownerAgent is a session agent (no dirLimits) with a session key to hand out.
func ownerAgent(t *testing.T) *Agent {
	t.Helper()
	key, err := crypto.NewSessionKey()
	if err != nil {
		t.Fatal(err)
	}
	return &Agent{sessionID: "TESTSESS", sessionKey: key}
}

// ownInit builds the message an enrolled device sends to connect PIN-free.
func ownInit(t *testing.T, sid string, priv ed25519.PrivateKey) protocol.Message {
	t.Helper()
	pub := priv.Public().(ed25519.PublicKey)
	eph, err := crypto.NewEphemeralKey()
	if err != nil {
		t.Fatal(err)
	}
	exHex, _, err := crypto.NewExID()
	if err != nil {
		t.Fatal(err)
	}
	vEph := eph.PublicKey().Bytes()
	return protocol.Message{
		Type:      protocol.TypeOwnerInit,
		ExID:      exHex,
		Data:      base64.StdEncoding.EncodeToString(vEph),
		DevicePub: base64.StdEncoding.EncodeToString(pub),
		DeviceSig: base64.StdEncoding.EncodeToString(crypto.SignOwner(priv, crypto.OwnerClientTranscript(sid, vEph, pub))),
	}
}

func nextOwnerMsg(t *testing.T, got <-chan protocol.Message) (protocol.Message, bool) {
	t.Helper()
	select {
	case m := <-got:
		return m, true
	case <-time.After(2 * time.Second):
		return protocol.Message{}, false
	}
}

// An owner's PIN-free connect used to be charged to the PIN-guess bucket, so
// someone reconnecting — a phone changing networks, a tab reopening — spent
// the same eight tokens an attacker guessing PINs would, then got silence.
// The viewer reads silence as "not an owner", so a few reconnects told
// someone they did not own their own machine.
func TestOwnerHandshakeIsNotChargedToThePINBudget(t *testing.T) {
	isolateHome(t)
	a := ownerAgent(t)

	// Spend the entire PIN allowance, as failed PIN connects would.
	now := time.Now()
	for i := 0; i < kexBurst; i++ {
		if !a.allowKex(now) {
			t.Fatalf("kex token %d should be available", i)
		}
	}
	if a.allowKex(now) {
		t.Fatal("the PIN bucket should be drained now")
	}

	// The owner path must be untouched by that.
	if !a.allowOwnerHandshake() {
		t.Fatal("an owner proof was refused because the PIN bucket was empty")
	}

	// And end to end: an enrolled device still gets its session key.
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	if _, _, err := AddOwner(ownerID(pub), "phone"); err != nil {
		t.Fatal(err)
	}
	conn, got := ownerConn(t)
	msg := ownInit(t, a.sessionID, priv)
	a.handleOwnerInit(conn, msg)
	reply, ok := nextOwnerMsg(t, got)
	if !ok {
		t.Fatal("no reply to an enrolled owner while the PIN bucket was empty")
	}
	if reply.Type != protocol.TypeOwnerResp || reply.ExID != msg.ExID || reply.Wrap == "" {
		t.Fatalf("want own_resp with a wrapped key, got %+v", reply)
	}
}

// A proof that checks out gives its token back, so an owner reconnecting all
// day never runs itself out of its own machine.
func TestProvenOwnerNeverExhaustsTheAllowance(t *testing.T) {
	isolateHome(t)
	a := ownerAgent(t)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	if _, _, err := AddOwner(ownerID(pub), "phone"); err != nil {
		t.Fatal(err)
	}
	conn, got := ownerConn(t)

	// Well past the burst, all at the same instant: nothing refills here, so
	// only the refund can keep these answered.
	for i := 0; i < ownVerifyBurst*3; i++ {
		msg := ownInit(t, a.sessionID, priv)
		a.handleOwnerInit(conn, msg)
		reply, ok := nextOwnerMsg(t, got)
		if !ok {
			t.Fatalf("owner handshake %d went unanswered", i)
		}
		if reply.Type != protocol.TypeOwnerResp {
			t.Fatalf("handshake %d: want own_resp, got %q", i, reply.Type)
		}
	}
}

// A device that cannot prove it is an owner still gets silence — that it is
// not enrolled is not something we confirm to it.
func TestUnenrolledDeviceGetsSilence(t *testing.T) {
	isolateHome(t)
	a := ownerAgent(t)
	_, priv, _ := ed25519.GenerateKey(rand.Reader) // never enrolled
	conn, got := ownerConn(t)

	a.handleOwnerInit(conn, ownInit(t, a.sessionID, priv))
	if m, ok := nextOwnerMsg(t, got); ok {
		t.Fatalf("an unenrolled device was answered with %+v", m)
	}
}

// Over the allowance, the agent says so. Silence there is what made a
// throttled owner look like a stranger; "busy, come back" tells a caller only
// what waiting would have told it anyway.
func TestOverTheAllowanceTheAgentSaysBusy(t *testing.T) {
	isolateHome(t)
	a := ownerAgent(t)
	_, priv, _ := ed25519.GenerateKey(rand.Reader) // unprovable: never refunded
	conn, got := ownerConn(t)

	for i := 0; i < ownVerifyBurst; i++ {
		a.handleOwnerInit(conn, ownInit(t, a.sessionID, priv))
	}
	// Drain anything those wrote (they should have written nothing).
	for len(got) > 0 {
		if m := <-got; m.Type != protocol.TypeOwnerBusy {
			t.Fatalf("unprovable handshake answered with %+v", m)
		}
	}
	a.handleOwnerInit(conn, ownInit(t, a.sessionID, priv))
	reply, ok := nextOwnerMsg(t, got)
	if !ok {
		t.Fatal("a throttled handshake got silence — the viewer reads that as 'not an owner'")
	}
	if reply.Type != protocol.TypeOwnerBusy {
		t.Fatalf("want own_busy, got %q", reply.Type)
	}
	if reply.RetryMS <= 0 {
		t.Fatalf("own_busy must say when to come back, got retry_ms=%d", reply.RetryMS)
	}
}

// The machine channel has its own, wider bucket; a refund there must go back
// to that one rather than to the session's.
func TestTokenBucketRefund(t *testing.T) {
	tb := newTokenBucket(2, 0) // no refill: only an explicit refund returns one
	now := time.Now()
	if !tb.allow(now) || !tb.allow(now) {
		t.Fatal("both tokens should be available")
	}
	if tb.allow(now) {
		t.Fatal("bucket should be empty")
	}
	tb.refund()
	if !tb.allow(now) {
		t.Fatal("a refunded token should be spendable again")
	}
	// Refunds never inflate the bucket past its maximum.
	for i := 0; i < 10; i++ {
		tb.refund()
	}
	if !tb.allow(now) || !tb.allow(now) {
		t.Fatal("bucket should hold its maximum after over-refunding")
	}
	if tb.allow(now) {
		t.Fatal("refunds must not raise the ceiling")
	}
}
