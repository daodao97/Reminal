// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package crypto

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
)

func TestOwnerTranscriptsBindAndDiffer(t *testing.T) {
	ve := bytes.Repeat([]byte{1}, 32)
	ae := bytes.Repeat([]byte{2}, 32)
	dp := bytes.Repeat([]byte{3}, 32)
	mp := bytes.Repeat([]byte{4}, 32)

	// Deterministic.
	if !bytes.Equal(OwnerClientTranscript("S", ve, dp), OwnerClientTranscript("S", ve, dp)) {
		t.Fatal("client transcript not deterministic")
	}
	if !bytes.Equal(OwnerServerTranscript("S", ve, ae, dp, mp), OwnerServerTranscript("S", ve, ae, dp, mp)) {
		t.Fatal("server transcript not deterministic")
	}
	// Client and server transcripts must never coincide (distinct domain tags).
	if bytes.Equal(OwnerClientTranscript("S", ve, dp), OwnerServerTranscript("S", ve, ae, dp, mp)) {
		t.Fatal("client and server transcripts collide")
	}
	// Every server field is bound.
	baseS := OwnerServerTranscript("S", ve, ae, dp, mp)
	variants := [][]byte{
		OwnerServerTranscript("X", ve, ae, dp, mp),
		OwnerServerTranscript("S", bytes.Repeat([]byte{9}, 32), ae, dp, mp),
		OwnerServerTranscript("S", ve, bytes.Repeat([]byte{9}, 32), dp, mp),
		OwnerServerTranscript("S", ve, ae, bytes.Repeat([]byte{9}, 32), mp),
		OwnerServerTranscript("S", ve, ae, dp, bytes.Repeat([]byte{9}, 32)),
	}
	for i, v := range variants {
		if bytes.Equal(baseS, v) {
			t.Errorf("server variant %d did not change the digest", i)
		}
	}
	// Length-prefixing: a byte moved across a field boundary must differ.
	if bytes.Equal(OwnerClientTranscript("A", []byte("BC"), nil), OwnerClientTranscript("AB", []byte("C"), nil)) {
		t.Fatal("field boundaries are ambiguous (missing length prefix)")
	}
}

func TestSignVerifyOwner(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	tr := OwnerClientTranscript("S", []byte("e1"), pub)
	sig := SignOwner(priv, tr)
	if !VerifyOwner(pub, tr, sig) {
		t.Fatal("valid signature rejected")
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if VerifyOwner(other, tr, sig) {
		t.Fatal("accepted signature under the wrong key")
	}
	bad := append([]byte(nil), tr...)
	bad[0] ^= 0xff
	if VerifyOwner(pub, bad, sig) {
		t.Fatal("accepted signature over a tampered transcript")
	}
	badSig := append([]byte(nil), sig...)
	badSig[0] ^= 0xff
	if VerifyOwner(pub, tr, badSig) {
		t.Fatal("accepted a tampered signature")
	}
	if VerifyOwner(ed25519.PublicKey{1, 2, 3}, tr, sig) {
		t.Fatal("accepted a malformed public key")
	}
}

// End-to-end 2-message owner handshake: device signs the client transcript,
// machine signs the server transcript, both verify, and the session key wraps
// and unwraps over the shared ECDH secret.
func TestOwnerHandshakeEndToEnd(t *testing.T) {
	viewerEph, _ := NewEphemeralKey()
	agentEph, _ := NewEphemeralKey()
	devPub, devPriv, _ := ed25519.GenerateKey(rand.Reader)
	macPub, macPriv, _ := ed25519.GenerateKey(rand.Reader)
	vEph := viewerEph.PublicKey().Bytes()
	aEph := agentEph.PublicKey().Bytes()
	const sess = "SESS"

	// msg1: device signs the client transcript.
	sigDev := SignOwner(devPriv, OwnerClientTranscript(sess, vEph, devPub))
	// agent verifies it.
	if !VerifyOwner(devPub, OwnerClientTranscript(sess, vEph, devPub), sigDev) {
		t.Fatal("agent rejected a valid device signature")
	}

	// msg2: machine signs the server transcript.
	sigMac := SignOwner(macPriv, OwnerServerTranscript(sess, vEph, aEph, devPub, macPub))
	if !VerifyOwner(macPub, OwnerServerTranscript(sess, vEph, aEph, devPub, macPub), sigMac) {
		t.Fatal("viewer rejected a valid machine signature")
	}

	// Shared secret + session-key delivery.
	aPub, _ := PeerPublicKey(aEph)
	vPub, _ := PeerPublicKey(vEph)
	sharedV, _ := viewerEph.ECDH(aPub)
	sharedA, _ := agentEph.ECDH(vPub)
	if !bytes.Equal(sharedV, sharedA) {
		t.Fatal("ECDH shared secret mismatch")
	}
	_, exID, _ := NewExID()
	sk := make([]byte, 32)
	_, _ = rand.Read(sk)
	wrapped, err := WrapSessionKey(sharedA, exID, sk)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnwrapSessionKey(sharedV, exID, wrapped)
	if err != nil || !bytes.Equal(got, sk) {
		t.Fatalf("session key delivery failed: %v", err)
	}
}

func TestDeriveDirectoryID(t *testing.T) {
	pubA, _, _ := ed25519.GenerateKey(rand.Reader)
	pubB, _, _ := ed25519.GenerateKey(rand.Reader)

	idA := DeriveDirectoryID(pubA)
	// Deterministic.
	if idA != DeriveDirectoryID(pubA) {
		t.Fatal("DeriveDirectoryID is not deterministic")
	}
	// Distinct machines → distinct channels.
	if idA == DeriveDirectoryID(pubB) {
		t.Fatal("different machine keys produced the same directory id")
	}
	// Right length and charset (so it can't collide with an 8-char session id).
	if len(idA) != DirectoryIDLen {
		t.Fatalf("directory id length = %d, want %d", len(idA), DirectoryIDLen)
	}
	for _, c := range idA {
		if !strings.ContainsRune(directoryAlphabet, c) {
			t.Fatalf("directory id has out-of-alphabet char %q", c)
		}
	}
}

func TestDirectoryTokenStableAndKeyed(t *testing.T) {
	_, privA, _ := ed25519.GenerateKey(rand.Reader)
	_, privB, _ := ed25519.GenerateKey(rand.Reader)
	if DirectoryToken(privA) != DirectoryToken(privA) {
		t.Fatal("DirectoryToken is not stable for the same key")
	}
	if DirectoryToken(privA) == DirectoryToken(privB) {
		t.Fatal("different machine keys produced the same directory token")
	}
}
