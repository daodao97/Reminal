// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package crypto

import (
	"crypto/ed25519"
	"testing"
)

// FuzzUntrustedInput feeds arbitrary bytes to every crypto entry point that
// processes viewer/relay-supplied data during a session or owner handshake. None
// may panic — a panic here is a remote crash an unauthenticated or malicious peer
// could trigger by sending malformed key material / ciphertext through the relay.
func FuzzUntrustedInput(f *testing.F) {
	seeds := [][3][]byte{
		{[]byte("AAAA"), []byte("deadbeef"), []byte{1, 2, 3}},
		{[]byte(""), []byte(""), []byte("")},
		{make([]byte, 32), make([]byte, 32), make([]byte, 48)},
		{make([]byte, 12), make([]byte, 0), make([]byte, 16)},
	}
	for _, s := range seeds {
		f.Add(s[0], s[1], s[2])
	}
	// A valid box (fixed 32-byte key); we fuzz the ciphertext it's asked to open.
	box, _ := NewBox(make([]byte, 32))

	f.Fuzz(func(t *testing.T, a, b, c []byte) {
		// box.Decrypt — viewers send base64 ciphertext through the relay.
		if box != nil {
			_, _ = box.Decrypt(string(a))
		}
		// ParseExID — viewer-supplied hex ex_id on the kex.
		_, _ = ParseExID(string(a))
		// PeerPublicKey — viewer ephemeral X25519 key bytes.
		_, _ = PeerPublicKey(a)
		// UnwrapSessionKey — (shared, exID, wrapped) as they arrive on the wire.
		_, _ = UnwrapSessionKey(a, b, c)
		// VerifyOwner — owner-connect signature check with attacker-controlled bytes.
		_ = VerifyOwner(ed25519.PublicKey(a), b, c)
	})
}
