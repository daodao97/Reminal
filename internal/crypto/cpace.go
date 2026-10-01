// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package crypto

// CPace: a balanced password-authenticated key exchange
// (draft-irtf-cfrg-cpace), over the ristretto255 group.
//
// The short secret a person types — a session PIN, a copy/paste code — never
// touches anything that goes over the wire. It only chooses the generator both
// sides do Diffie-Hellman over:
//
//	G = hash_to_group(secret, channel, sid)
//	initiator: Ya = ya·G        responder: Yb = yb·G
//	shared     = ya·Yb = yb·Ya
//
// Ya and Yb are uniformly distributed group elements whatever the secret is,
// so a transcript gives nobody anything to test a guess against. The only way
// to try a secret is to take part in a handshake with it — one guess per
// handshake, and each handshake is rate-limited by whoever answers it.
//
// The derived key binds the whole transcript (channel, sid, both elements in
// order), so an element cannot be lifted from one handshake into another and a
// party cannot be tricked into agreeing with itself.

import (
	"crypto/rand"
	"crypto/sha512"
	"encoding/binary"
	"errors"

	"github.com/gtank/ristretto255"
)

// cpaceDSI is the domain separator for the generator, fixed by the spec for
// this group.
const cpaceDSI = "CPaceRistretto255"

// CPaceElementBytes is the encoded size of a ristretto255 element on the wire.
const CPaceElementBytes = 32

// lv length-prefixes each field so no two different inputs can concatenate to
// the same bytes ("ab"+"c" vs "a"+"bc").
func lv(fields ...[]byte) []byte {
	var out []byte
	var n [8]byte
	for _, f := range fields {
		binary.BigEndian.PutUint64(n[:], uint64(len(f)))
		out = append(out, n[:]...)
		out = append(out, f...)
	}
	return out
}

// cpaceGenerator maps (secret, channel, sid) to a group element. Different
// secrets give unrelated generators, and nobody can find the discrete log
// between two of them — which is the whole of why a wrong guess learns nothing.
func cpaceGenerator(secret, channel, sid []byte) *ristretto255.Element {
	h := sha512.Sum512(lv([]byte(cpaceDSI), secret, channel, sid))
	return ristretto255.NewElement().FromUniformBytes(h[:])
}

// CPaceState is one side's half of a handshake, held until the peer answers.
type CPaceState struct {
	y       *ristretto255.Scalar
	mine    []byte // our element, as sent
	channel []byte
	sid     []byte
	init    bool // whether we are the initiator; fixes transcript order
}

// NewCPace starts a handshake and returns our element to send.
//
// channel separates uses of the same secret (a session PIN is not a copy code);
// sid is everything both sides already agree on for this handshake — session
// id and the handshake's own random id — so an element from one handshake is
// worthless in any other.
func NewCPace(secret, channel, sid []byte, initiator bool) (*CPaceState, []byte, error) {
	if len(secret) == 0 {
		return nil, nil, errors.New("cpace: empty secret")
	}
	var seed [64]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return nil, nil, err
	}
	y := ristretto255.NewScalar().FromUniformBytes(seed[:])
	G := cpaceGenerator(secret, channel, sid)
	Y := ristretto255.NewElement().ScalarMult(y, G)
	mine := Y.Encode(nil)
	return &CPaceState{y: y, mine: mine, channel: channel, sid: sid, init: initiator}, mine, nil
}

var errCPacePeer = errors.New("cpace: peer element rejected")

// Finish takes the peer's element and returns the shared key. It refuses an
// element that does not decode or is the identity: a peer sending the identity
// would make the shared secret the identity too, which an attacker knows
// without knowing the secret.
func (s *CPaceState) Finish(peer []byte) ([]byte, error) {
	if s == nil || s.y == nil {
		return nil, errors.New("cpace: handshake already finished")
	}
	if len(peer) != CPaceElementBytes {
		return nil, errCPacePeer
	}
	P := ristretto255.NewElement()
	if err := P.Decode(peer); err != nil {
		return nil, errCPacePeer
	}
	identity := ristretto255.NewElement().Zero()
	if P.Equal(identity) == 1 {
		return nil, errCPacePeer
	}
	K := ristretto255.NewElement().ScalarMult(s.y, P)
	if K.Equal(identity) == 1 {
		return nil, errCPacePeer
	}
	// The scalar is single-use: clear it so a second Finish cannot reuse it.
	s.y = nil

	ya, yb := s.mine, peer
	if !s.init {
		ya, yb = peer, s.mine
	}
	isk := sha512.Sum512(lv([]byte(cpaceDSI+"_ISK"), s.channel, s.sid, K.Encode(nil), ya, yb))
	return isk[:32], nil
}
