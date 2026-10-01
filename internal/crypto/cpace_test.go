// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package crypto

import (
	"bytes"
	"testing"

	"github.com/gtank/ristretto255"
)

func cpaceRun(t *testing.T, a, b, chA, chB, sidA, sidB string) ([]byte, []byte, error) {
	t.Helper()
	sa, ya, err := NewCPace([]byte(a), []byte(chA), []byte(sidA), true)
	if err != nil {
		t.Fatal(err)
	}
	sb, yb, err := NewCPace([]byte(b), []byte(chB), []byte(sidB), false)
	if err != nil {
		t.Fatal(err)
	}
	ka, err := sa.Finish(yb)
	if err != nil {
		return nil, nil, err
	}
	kb, err := sb.Finish(ya)
	if err != nil {
		return nil, nil, err
	}
	return ka, kb, nil
}

func TestCPaceBothSidesAgree(t *testing.T) {
	ka, kb, err := cpaceRun(t, "428913", "428913", "c", "c", "s", "s")
	if err != nil {
		t.Fatal(err)
	}
	if len(ka) != 32 || !bytes.Equal(ka, kb) {
		t.Fatalf("keys differ or wrong size: %x / %x", ka, kb)
	}
}

// Anything the two sides do not share must give different keys.
func TestCPaceKeyDependsOnEverythingShared(t *testing.T) {
	cases := []struct{ name, a, b, chA, chB, sA, sB string }{
		{"different secret", "428913", "428914", "c", "c", "s", "s"},
		{"different channel", "428913", "428913", "session", "copy", "s", "s"},
		{"different sid", "428913", "428913", "c", "c", "s1", "s2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ka, kb, err := cpaceRun(t, c.a, c.b, c.chA, c.chB, c.sA, c.sB)
			if err != nil {
				return // refusing outright is also a mismatch
			}
			if bytes.Equal(ka, kb) {
				t.Fatal("keys matched although the inputs did not")
			}
		})
	}
}

// Two runs with the same inputs must not produce the same elements or keys:
// the scalar is fresh each time.
func TestCPaceIsFreshEachTime(t *testing.T) {
	_, y1, _ := NewCPace([]byte("p"), []byte("c"), []byte("s"), true)
	_, y2, _ := NewCPace([]byte("p"), []byte("c"), []byte("s"), true)
	if bytes.Equal(y1, y2) {
		t.Fatal("two handshakes sent the same element")
	}
}

func TestCPaceRejectsBadPeerElements(t *testing.T) {
	identity := ristretto255.NewElement().Zero().Encode(nil)
	nonCanonical := bytes.Repeat([]byte{0xff}, 32)
	for name, peer := range map[string][]byte{
		"identity":      identity,
		"not canonical": nonCanonical,
		"too short":     make([]byte, 31),
		"too long":      make([]byte, 33),
		"empty":         nil,
	} {
		t.Run(name, func(t *testing.T) {
			s, _, _ := NewCPace([]byte("p"), []byte("c"), []byte("s"), true)
			if _, err := s.Finish(peer); err == nil {
				t.Fatalf("accepted a %s peer element", name)
			}
		})
	}
}

func TestCPaceFinishIsSingleUse(t *testing.T) {
	a, _, _ := NewCPace([]byte("p"), []byte("c"), []byte("s"), true)
	_, yb, _ := NewCPace([]byte("p"), []byte("c"), []byte("s"), false)
	if _, err := a.Finish(yb); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Finish(yb); err == nil {
		t.Fatal("a handshake finished twice")
	}
}

func TestCPaceRefusesEmptySecret(t *testing.T) {
	if _, _, err := NewCPace(nil, []byte("c"), []byte("s"), true); err == nil {
		t.Fatal("started a handshake with no secret")
	}
}

// Length-prefixing keeps ("ab","c") and ("a","bc") apart.
func TestLVIsUnambiguous(t *testing.T) {
	if bytes.Equal(lv([]byte("ab"), []byte("c")), lv([]byte("a"), []byte("bc"))) {
		t.Fatal("two different field splits encoded the same")
	}
}
