// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package crypto

import (
	"bytes"
	"testing"
)

// The wrap layer on its own: a session key wrapped under a handshake key and
// exchange id opens only under the same pair. The handshake that produces the
// key is tested in cpace_test.go.
func TestWrapSessionKeyRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	sk := bytes.Repeat([]byte{9}, 32)
	_, exID, err := NewExID()
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := WrapSessionKey(key, exID, sk)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnwrapSessionKey(key, exID, wrapped)
	if err != nil || !bytes.Equal(got, sk) {
		t.Fatalf("round trip failed: %v", err)
	}
	other := bytes.Repeat([]byte{8}, 32)
	if _, err := UnwrapSessionKey(other, exID, wrapped); err == nil {
		t.Fatal("unwrapped under a different key")
	}
	_, exID2, _ := NewExID()
	if _, err := UnwrapSessionKey(key, exID2, wrapped); err == nil {
		t.Fatal("unwrapped under a different exchange id")
	}
	wrapped[len(wrapped)-1] ^= 1
	if _, err := UnwrapSessionKey(key, exID, wrapped); err == nil {
		t.Fatal("unwrapped a tampered wrap")
	}
}

// TestBoxRoundTrip: the AEAD wrapper around a session key encrypts
// and decrypts correctly.
func TestBoxRoundTrip(t *testing.T) {
	key, err := NewSessionKey()
	if err != nil {
		t.Fatalf("session key: %v", err)
	}
	box, err := NewBox(key)
	if err != nil {
		t.Fatalf("new box: %v", err)
	}
	plaintext := []byte("hello reminal")
	enc, err := box.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	dec, err := box.Decrypt(enc)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(dec, plaintext) {
		t.Fatalf("plaintext mismatch: got %q, want %q", dec, plaintext)
	}
}
