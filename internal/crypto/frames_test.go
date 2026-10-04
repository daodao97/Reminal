// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package crypto

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func frameKeys(t *testing.T) *FrameKeys {
	t.Helper()
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	fk, err := NewFrameKeys(k)
	if err != nil {
		t.Fatal(err)
	}
	return fk
}

// A frame opens only with exactly what it was sealed with.
func TestFrameOpensOnlyAsSealed(t *testing.T) {
	k := frameKeys(t)
	data, err := k.Seal(FromViewer, "data", 3, 7, 0, FrameBoxed, []byte("ls\r"))
	if err != nil {
		t.Fatal(err)
	}
	kind, pt, err := k.Open(FromViewer, "data", 3, 7, 0, data)
	if err != nil || kind != FrameBoxed || !bytes.Equal(pt, []byte("ls\r")) {
		t.Fatalf("round trip: kind=%d pt=%q err=%v", kind, pt, err)
	}
	for name, open := range map[string]func() error{
		"other direction": func() error { _, _, e := k.Open(FromAgent, "data", 3, 7, 0, data); return e },
		"other type":      func() error { _, _, e := k.Open(FromViewer, "notify", 3, 7, 0, data); return e },
		"other stream":    func() error { _, _, e := k.Open(FromViewer, "data", 4, 7, 0, data); return e },
		"other counter":   func() error { _, _, e := k.Open(FromViewer, "data", 3, 8, 0, data); return e },
		"other seq":       func() error { _, _, e := k.Open(FromViewer, "data", 3, 7, 1, data); return e },
		"other session":   func() error { _, _, e := frameKeys(t).Open(FromViewer, "data", 3, 7, 0, data); return e },
	} {
		if open() == nil {
			t.Errorf("%s: frame opened", name)
		}
	}
}

func TestSealInfoRoundTrip(t *testing.T) {
	shared := make([]byte, 32)
	_, _ = rand.Read(shared)
	exID := []byte("0123456789abcdef")
	s, err := SealSealInfo(shared, exID, SealInfo{Stream: 9, AgentAt: 1 << 40})
	if err != nil {
		t.Fatal(err)
	}
	got, err := OpenSealInfo(shared, exID, s)
	if err != nil || got.Stream != 9 || got.AgentAt != 1<<40 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := OpenSealInfo(shared, []byte("fedcba9876543210"), s); err == nil {
		t.Fatal("seal info opened for another exchange")
	}
}

// The agent's own nonces never repeat, and are recognised as its own.
func TestOwnNoncesAreDistinctAndMarked(t *testing.T) {
	k := frameKeys(t)
	seen := map[string]bool{}
	for i := 0; i < 10000; i++ {
		n, err := k.OwnNonce()
		if err != nil {
			t.Fatal(err)
		}
		if seen[string(n)] {
			t.Fatalf("nonce repeated after %d", i)
		}
		seen[string(n)] = true
		if !k.IsOwnNonce(n) {
			t.Fatal("own nonce not recognised")
		}
	}
	other := make([]byte, 12)
	if k.IsOwnNonce(other) || frameKeys(t).IsOwnNonce(func() []byte { n, _ := k.OwnNonce(); return n }()) {
		t.Fatal("a nonce not made by this key was recognised")
	}
}
