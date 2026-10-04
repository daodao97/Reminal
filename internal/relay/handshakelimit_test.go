// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package relay

import (
	"testing"
	"time"
)

func TestHandshakesPerAddress(t *testing.T) {
	s := NewServer()
	now := time.Now()
	for i := 0; i < handshakeBurst; i++ {
		if !s.takeHandshake("SESS|10.0.0.1", now) {
			t.Fatalf("handshake %d from one address refused within the burst", i+1)
		}
	}
	if s.takeHandshake("SESS|10.0.0.1", now) {
		t.Fatal("handshake past the burst allowed")
	}
	// Another address, and the same address on another session, are separate.
	if !s.takeHandshake("SESS|10.0.0.2", now) || !s.takeHandshake("OTHER|10.0.0.1", now) {
		t.Fatal("one address's handshakes counted against another")
	}
	// It refills.
	if !s.takeHandshake("SESS|10.0.0.1", now.Add(handshakeRefill)) {
		t.Fatal("no handshake allowed after a refill interval")
	}
}
