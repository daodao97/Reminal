// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package crypto

// Session-key wrapping and handshake helpers.
//
// The PIN handshake itself is CPace (cpace.go). What lives here is what
// surrounds it: the per-handshake exchange id, wrapping the session key under
// the key a handshake derives, and the X25519 helpers the owner (PIN-free)
// handshake uses.
//
// History, kept on purpose. The v1 wire key was derived directly from
// (PIN, sessionID) via HKDF. The sessionID is the relay's routing key, so the
// only secret was the 6-digit PIN (~20 bits): a relay that recorded a single
// ciphertext frame could try all 10^6 PINs offline against the AES-GCM tag and
// recover the session key. See GitHub issue #1. Since then the session key has
// been random and delivered by an authenticated handshake; that handshake is
// now CPace.
//
// The exchange id (ex_id) the initiator picks per handshake is the HKDF salt
// for the wrap key and is echoed in the reply. With several viewers, the relay
// broadcasts the agent's reply to all of them; the ex_id is how each
// recognises its own. (Another viewer could not unwrap it anyway — different
// shared secret — but matching avoids pointless decryption attempts.)

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

// Domain separation for the wrap-key derivation (salt is per-handshake
// ex_id; info is this constant).
var wrapInfo = []byte("reminal-wrap-v2")

// PubKeyBytes is the length of an X25519 public key.
const PubKeyBytes = 32

// ExIDBytes is the length the viewer should pick for the random
// per-handshake correlation ID. Long enough that two concurrent
// handshakes from different viewers won't collide.
const ExIDBytes = 16

// NewEphemeralKey returns a fresh X25519 keypair for one handshake.
func NewEphemeralKey() (*ecdh.PrivateKey, error) {
	return ecdh.X25519().GenerateKey(rand.Reader)
}

// NewExID returns a fresh random per-handshake correlation ID,
// hex-encoded for use in the wire `ex_id` field.
func NewExID() (string, []byte, error) {
	raw := make([]byte, ExIDBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	return hex.EncodeToString(raw), raw, nil
}

// ParseExID decodes the hex form back to bytes. Returns an error if
// the encoding is malformed or the length looks suspicious; we accept
// any length that came from a real client to stay tolerant of future
// minor bumps, but reject empty or absurdly long IDs that a malicious
// relay could try to feed us.
func ParseExID(hexStr string) ([]byte, error) {
	if hexStr == "" {
		return nil, errors.New("ex_id missing")
	}
	if len(hexStr) > 128 {
		return nil, errors.New("ex_id too long")
	}
	return hex.DecodeString(hexStr)
}

// wrapKey derives the AES-256-GCM key that wraps the session key.
// salt is the handshake's ex_id (so two concurrent handshakes derive
// independent wrap keys even if — somehow — the ECDH shared collides).
func wrapKey(shared, exID []byte) ([]byte, error) {
	r := hkdf.New(sha256.New, shared, exID, wrapInfo)
	key := make([]byte, 32)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, err
	}
	return key, nil
}

// WrapSessionKey encrypts a 32-byte session key under the AES-256-GCM
// key derived from (shared, exID). Output is nonce ‖ ciphertext.
func WrapSessionKey(shared, exID, sessionKey []byte) ([]byte, error) {
	if len(sessionKey) != 32 {
		return nil, fmt.Errorf("session key must be 32 bytes, got %d", len(sessionKey))
	}
	key, err := wrapKey(shared, exID)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ct := aead.Seal(nil, nonce, sessionKey, nil)
	out := make([]byte, 0, len(nonce)+len(ct))
	out = append(out, nonce...)
	out = append(out, ct...)
	return out, nil
}

// UnwrapSessionKey decrypts a wrap produced by WrapSessionKey. A
// failure here means the peer used a different PIN, the relay tried
// an active MITM with the wrong guess, or the bytes were corrupted.
func UnwrapSessionKey(shared, exID, wrapped []byte) ([]byte, error) {
	key, err := wrapKey(shared, exID)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(wrapped) < aead.NonceSize()+aead.Overhead() {
		return nil, errors.New("wrap: ciphertext too short")
	}
	nonce, ct := wrapped[:aead.NonceSize()], wrapped[aead.NonceSize():]
	pt, err := aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("wrap: %w", err)
	}
	if len(pt) != 32 {
		return nil, fmt.Errorf("wrap: unexpected session key length %d", len(pt))
	}
	return pt, nil
}

// PeerPublicKey wraps a 32-byte X25519 public key for use with
// ecdh.PrivateKey.ECDH. Rejects low-order / invalid points (which
// crypto/ecdh's X25519 curve also flags, so this is mostly defensive
// labelling).
func PeerPublicKey(raw []byte) (*ecdh.PublicKey, error) {
	if len(raw) != PubKeyBytes {
		return nil, fmt.Errorf("peer key: wrong length %d", len(raw))
	}
	return ecdh.X25519().NewPublicKey(raw)
}
