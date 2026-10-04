// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"crypto/rand"
	"fmt"
	"testing"
	"time"

	"reminal/internal/crypto"
	"reminal/internal/protocol"
)

// relayPace is the relay's per-address pacing (internal/relay/handshakelimit.go):
// what one source can send at most.
type relayPace struct {
	tokens float64
	at     time.Time
}

func (r *relayPace) take(now time.Time) bool {
	const burst, refill = 10, time.Minute
	if r.at.IsZero() {
		r.tokens = burst
	} else {
		r.tokens = min(burst, r.tokens+now.Sub(r.at).Seconds()/refill.Seconds())
	}
	r.at = now
	if r.tokens < 1 {
		return false
	}
	r.tokens--
	return true
}

// simulate runs three hours: n sources asking as often as the relay lets
// them, and one source asking every 3 min that proved the PIN (proven) or
// did not. It returns how many of the latter's 60 asks were answered and the
// longest run of time between answers.
func simulate(n int, proven bool) (answered int, longestGap time.Duration) {
	a := &Agent{}
	base := time.Now()
	if proven {
		a.markProvenSource("H")
	}
	pace := make([]relayPace, n)
	last := base
	for sec := 0; sec < 3*3600; sec += 10 {
		now := base.Add(time.Duration(sec) * time.Second)
		for i := range pace {
			if pace[i].take(now) {
				a.allowKexFrom(now, fmt.Sprintf("S%d", i))
			}
		}
		// Asks between the others' refill moments, not in step with them.
		if sec%180 != 10 {
			continue
		}
		if ok, _ := a.allowKexFrom(now, "H"); ok {
			answered++
			last = now
		} else if g := now.Sub(last); g > longestGap {
			longestGap = g
		}
	}
	return answered, longestGap
}

// A source that proved the PIN once keeps being answered at a steady pace,
// however many other sources are asking.
func TestKexProvenSourceKeepsBeingAnswered(t *testing.T) {
	for _, n := range []int{1, 2, 10, 2000} {
		got, gap := simulate(n, true)
		if got < 30 || gap > 15*time.Minute {
			t.Errorf("%d other sources: proven source answered %d of 60, longest gap %v", n, got, gap)
		}
		first, firstGap := simulate(n, false)
		t.Logf("%d other sources: proven %d/60 (gap %v); first-time %d/60 (gap %v)", n, got, gap, first, firstGap)
	}
}

// A source becomes proven only by a sealed frame opening on the stream its
// handshake was issued. Nothing without the PIN gets a source in.
func TestKexProvenNeedsThePIN(t *testing.T) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := crypto.NewBox(key)
	a := &Agent{sessionKey: key, box: box}
	shared := make([]byte, 32)
	exID := []byte("0123456789abcdef")
	v := &Viewer{box: box}
	if err := v.setSeal(key, shared, exID, a.sealInfoFor(1, shared, exID, "ATTACKER")); err != nil {
		t.Fatal(err)
	}
	// Bare read-only request.
	a.admit(protocol.Message{Type: protocol.TypeDirQuery, Src: "ATTACKER"})
	// The agent's own frame sent back on the issued stream.
	back := fromAgent(t, a, protocol.Message{Type: protocol.TypeData, Data: "x"})
	back.Stream = 1
	a.admit(back)
	// An earlier-form message replayed (and one that is fresh: that proves a
	// PIN holder is attached, but names no source).
	enc, _ := box.Encrypt([]byte("y"))
	a.admit(protocol.Message{Type: protocol.TypeData, Data: enc})
	a.admit(protocol.Message{Type: protocol.TypeData, Data: enc})
	if len(a.kexProven) != 0 {
		t.Fatalf("proven sources without a PIN-bearing frame: %v", a.kexProven)
	}
	// A sealed frame on the stream: that needed the session key.
	if _, ok, _ := a.admit(fromViewer(t, v, protocol.Message{Type: protocol.TypeData, Data: "z"})); !ok {
		t.Fatal("viewer's own frame refused")
	}
	if !a.isProvenSource("ATTACKER") {
		t.Fatal("a source that opened a frame on its stream was not marked proven")
	}
}

// The earlier form is written for a short while after a handshake without
// sealed frames, and for as long as a viewer that proved the PIN with it is
// attached.
func TestLegacyFormGraceAndProof(t *testing.T) {
	a, _ := sealedPair(t)
	a.handshakeWithout(0)
	if !a.legacyWanted() {
		t.Fatal("not written right after the handshake")
	}
	a.seal.legacyAt.Store(time.Now().Add(-2 * legacyGrace).UnixNano())
	if a.legacyWanted() {
		t.Fatal("still written after the grace with no proof")
	}
	a.handshakeWithout(0)
	enc, _ := a.box.Encrypt([]byte(`{"cols":80,"rows":24}`))
	if _, ok, _ := a.admit(protocol.Message{Type: protocol.TypeResize, Data: enc}); !ok {
		t.Fatal("earlier-form message refused")
	}
	a.seal.legacyAt.Store(time.Now().Add(-2 * legacyGrace).UnixNano())
	if !a.legacyWanted() {
		t.Fatal("not written after proof, past the grace")
	}
	a.lastViewerLeft()
	if a.legacyWanted() {
		t.Fatal("still written after the last viewer left")
	}
}
