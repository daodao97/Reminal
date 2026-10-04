// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"crypto/rand"
	"encoding/json"
	"testing"

	"reminal/internal/crypto"
	"reminal/internal/protocol"
)

// sealedPair is an agent and a viewer that completed a handshake asking for
// sealed frames, with nothing between them: tests play the relay by hand.
func sealedPair(t *testing.T) (*Agent, *Viewer) {
	t.Helper()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, err := crypto.NewBox(key)
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{sessionKey: key, box: box}
	shared := make([]byte, 32)
	_, _ = rand.Read(shared)
	exID := []byte("0123456789abcdef")
	info := a.sealInfoFor(1, shared, exID)
	if info == "" {
		t.Fatal("agent issued no seal info")
	}
	v := &Viewer{box: box}
	if err := v.setSeal(key, shared, exID, info); err != nil {
		t.Fatal(err)
	}
	return a, v
}

// fromViewer is what the viewer puts on the wire for msg.
func fromViewer(t *testing.T, v *Viewer, msg protocol.Message) protocol.Message {
	t.Helper()
	if msg.Data != "" {
		enc, err := v.box.Encrypt([]byte(msg.Data))
		if err != nil {
			t.Fatal(err)
		}
		msg.Data = enc
	}
	v.writeMu.Lock()
	raw, err := v.sealOut(msg)
	v.writeMu.Unlock()
	if err != nil || raw == nil {
		t.Fatalf("viewer did not seal %s: %v", msg.Type, err)
	}
	var out protocol.Message
	_ = json.Unmarshal(raw, &out)
	return out
}

// fromAgent is what the agent puts on the wire for msg.
func fromAgent(t *testing.T, a *Agent, msg protocol.Message) protocol.Message {
	t.Helper()
	enc, err := a.box.Encrypt([]byte(msg.Data))
	if err != nil {
		t.Fatal(err)
	}
	msg.Data = enc
	a.writeMu.Lock()
	a.seal.outCtr++
	raw, err := a.sealedFrame(a.frameKeys(), msg, a.seal.outCtr)
	a.writeMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	var out protocol.Message
	_ = json.Unmarshal(raw, &out)
	return out
}

func plain(t *testing.T, box *crypto.Box, data string) string {
	t.Helper()
	pt, err := box.Decrypt(data)
	if err != nil {
		t.Fatalf("handler could not decrypt what admit passed on: %v", err)
	}
	return string(pt)
}

func TestAgentAcceptsAViewerFrameOnce(t *testing.T) {
	a, v := sealedPair(t)
	f := fromViewer(t, v, protocol.Message{Type: protocol.TypeData, Data: "ls\r"})
	got, ok, _ := a.admit(f)
	if !ok || got.Type != protocol.TypeData || plain(t, a.box, got.Data) != "ls\r" {
		t.Fatalf("first delivery: ok=%v type=%s", ok, got.Type)
	}
	if _, ok, _ := a.admit(f); ok {
		t.Fatal("the same keystroke frame was accepted twice")
	}
}

func TestAgentRefusesOutOfOrderViewerFrames(t *testing.T) {
	a, v := sealedPair(t)
	first := fromViewer(t, v, protocol.Message{Type: protocol.TypeData, Data: "a"})
	second := fromViewer(t, v, protocol.Message{Type: protocol.TypeData, Data: "b"})
	if _, ok, _ := a.admit(second); !ok {
		t.Fatal("in-order frame refused")
	}
	if _, ok, _ := a.admit(first); ok {
		t.Fatal("an earlier frame was accepted after a later one")
	}
}

func TestAgentRefusesARelabelledFrame(t *testing.T) {
	a, v := sealedPair(t)
	f := fromViewer(t, v, protocol.Message{Type: protocol.TypeData, Data: `{"cols":1,"rows":1}`})
	f.Inner = protocol.TypeResize
	if _, ok, _ := a.admit(f); ok {
		t.Fatal("a data frame was accepted as a resize")
	}
}

func TestAgentOutputIsNotViewerInput(t *testing.T) {
	a, _ := sealedPair(t)
	out := fromAgent(t, a, protocol.Message{Type: protocol.TypeData, Data: "echo hi\r", Seq: 5})
	out.Stream = 1 // the stream the handshake issued
	if _, ok, _ := a.admit(out); ok {
		t.Fatal("agent output was accepted as viewer input")
	}
}

func TestAgentRefusesAStreamNoHandshakeIssued(t *testing.T) {
	a, v := sealedPair(t)
	f := fromViewer(t, v, protocol.Message{Type: protocol.TypeData, Data: "x"})
	f.Stream = 99
	if _, ok, _ := a.admit(f); ok {
		t.Fatal("frame on an unissued stream accepted")
	}
}

// A viewer from before sealed frames still works, and the agent writes both
// forms while it is attached. Messages the agent wrote are not input.
func TestAgentOlderViewerAndItsEchoes(t *testing.T) {
	a, _ := sealedPair(t)
	legacy, _ := a.box.Encrypt([]byte(`{"cols":80,"rows":24}`))
	_, ok, legacyNow := a.admit(protocol.Message{Type: protocol.TypeResize, Data: legacy})
	if !ok || !legacyNow || !a.seal.legacy.Load() {
		t.Fatalf("older viewer's message: ok=%v legacyNow=%v", ok, legacyNow)
	}
	ownOutput, _ := a.box.Encrypt([]byte("echo hi\r"))
	a.rememberEcho(ownOutput)
	if _, ok, _ := a.admit(protocol.Message{Type: protocol.TypeData, Data: ownOutput}); ok {
		t.Fatal("agent's own unsealed output accepted as input")
	}
	if _, ok, _ := a.admit(protocol.Message{Type: protocol.TypeData, Data: "bm90IG91cnM="}); ok {
		t.Fatal("a message that is not encrypted with the session key was accepted")
	}
}

// With no older viewer attached, unsealed session messages are not accepted.
func TestAgentRefusesUnsealedWhenAllViewersSeal(t *testing.T) {
	a, _ := sealedPair(t)
	if _, ok, _ := a.admit(protocol.Message{Type: protocol.TypeNewSession}); ok {
		t.Fatal("an unsealed request with no payload was accepted")
	}
	if a.seal.legacy.Load() {
		t.Fatal("agent believes an older viewer is attached")
	}
}

// An older client asks for lists bare. That is answered, in the form it can
// read, and changes nothing else.
func TestAgentAnswersAnOlderClientsBareRequest(t *testing.T) {
	a, _ := sealedPair(t)
	_, ok, legacyNow := a.admit(protocol.Message{Type: protocol.TypeDirQuery})
	if !ok || !legacyNow {
		t.Fatalf("bare list request: ok=%v legacyNow=%v", ok, legacyNow)
	}
}

func TestViewerAcceptsAgentFrameOnceAndInOrder(t *testing.T) {
	a, v := sealedPair(t)
	f1 := fromAgent(t, a, protocol.Message{Type: protocol.TypeData, Data: "one", Seq: 1})
	f2 := fromAgent(t, a, protocol.Message{Type: protocol.TypeData, Data: "two", Seq: 2})
	got, ok, err := v.admitIn(f1)
	if err != nil || !ok || plain(t, v.box, got.Data) != "one" || got.Seq != 1 {
		t.Fatalf("first: ok=%v err=%v", ok, err)
	}
	if _, ok, _ := v.admitIn(f1); ok {
		t.Fatal("same agent frame accepted twice")
	}
	if _, ok, _ := v.admitIn(f2); !ok {
		t.Fatal("next agent frame refused")
	}
	f2.Inner = protocol.TypeNotify
	f2.Ctr = 99
	if _, ok, _ := v.admitIn(f2); ok {
		t.Fatal("a relabelled frame was accepted")
	}
}

// A viewer starts after the counter its handshake was given: what the agent
// sent before it joined cannot be played to it.
func TestViewerRefusesFramesFromBeforeItJoined(t *testing.T) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := crypto.NewBox(key)
	a := &Agent{sessionKey: key, box: box}
	earlier := fromAgent(t, a, protocol.Message{Type: protocol.TypeData, Data: "old"})
	shared := make([]byte, 32)
	_, _ = rand.Read(shared)
	exID := []byte("0123456789abcdef")
	v := &Viewer{box: box}
	if err := v.setSeal(key, shared, exID, a.sealInfoFor(1, shared, exID)); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := v.admitIn(earlier); ok {
		t.Fatal("a frame from before this viewer joined was accepted")
	}
}

func TestViewerInputIsNotAgentOutput(t *testing.T) {
	_, v := sealedPair(t)
	f := fromViewer(t, v, protocol.Message{Type: protocol.TypeData, Data: "x"})
	if _, ok, _ := v.admitIn(f); ok {
		t.Fatal("viewer's own frame accepted as agent output")
	}
}

func TestViewerTakesOnlySealedWhenSealing(t *testing.T) {
	_, v := sealedPair(t)
	enc, _ := v.box.Encrypt([]byte("legacy copy"))
	if _, ok, _ := v.admitIn(protocol.Message{Type: protocol.TypeData, Data: enc}); ok {
		t.Fatal("unsealed copy accepted by a sealing viewer")
	}
	if _, ok, _ := v.admitIn(protocol.Message{Type: protocol.TypeAgentOnline}); !ok {
		t.Fatal("relay's own message dropped")
	}
}

// A handshake answer without seal info, followed by sealed frames: the two
// disagree, so the viewer reconnects.
func TestViewerReconnectsWhenAnswerAndFramesDisagree(t *testing.T) {
	a, _ := sealedPair(t)
	v := &Viewer{box: a.box}
	if err := v.setSeal(a.sessionKey, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	f := fromAgent(t, a, protocol.Message{Type: protocol.TypeData, Data: "x"})
	if _, _, err := v.admitIn(f); err != errSealedDowngrade {
		t.Fatalf("got %v, want errSealedDowngrade", err)
	}
}
