// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync/atomic"

	"golang.org/x/crypto/hkdf"
)

// Sealed frames carry session messages once both ends support them. Each
// direction has its own key, derived from the session key, and every frame's
// associated data names its direction, its message type, the sender's stream,
// its position in that stream and its scrollback seq. A frame therefore opens
// only as the message it was sent as, in the direction it was sent, once.
//
// Agent → viewer frames use stream 0 and one counter per agent process. Each
// viewer handshake is given its own stream for viewer → agent frames, with a
// counter starting at 1. The handshake answer tells the viewer its stream and
// where the agent's counter stands (SealSealInfo), authenticated with the
// handshake's own key.

// Frame directions, bound into each frame.
const (
	FromAgent  byte = 1
	FromViewer byte = 2
)

var (
	frameInfoA2V = []byte("reminal-frame-v1 agent-to-viewer")
	frameInfoV2A = []byte("reminal-frame-v1 viewer-to-agent")
	frameAADTag  = []byte("reminal-frame-v1")
	sealInfoInfo = []byte("reminal-seal-info-v1")
	ownNonceInfo = []byte("reminal-frame-v1 own-nonce")
)

// FrameKeys holds the two directional keys for one session key, and the key
// that marks the nonces of the agent's own earlier-form messages.
type FrameKeys struct {
	a2v, v2a cipher.AEAD
	ownMark  []byte
	ownCtr   atomic.Uint64
}

func gcmFrom(ikm, info []byte) (cipher.AEAD, error) {
	k := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, ikm, nil, info), k); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// NewFrameKeys derives the directional frame keys from a session key.
func NewFrameKeys(sessionKey []byte) (*FrameKeys, error) {
	if len(sessionKey) != 32 {
		return nil, fmt.Errorf("session key must be 32 bytes, got %d", len(sessionKey))
	}
	a2v, err := gcmFrom(sessionKey, frameInfoA2V)
	if err != nil {
		return nil, err
	}
	v2a, err := gcmFrom(sessionKey, frameInfoV2A)
	if err != nil {
		return nil, err
	}
	mark := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, sessionKey, nil, ownNonceInfo), mark); err != nil {
		return nil, err
	}
	return &FrameKeys{a2v: a2v, v2a: v2a, ownMark: mark}, nil
}

// OwnNonce returns a fresh 12-byte nonce for a message the agent writes in the
// earlier (unsealed) form: an 8-byte counter and 4 bytes of a MAC over it, so
// the agent can tell such a message is one of its own from the nonce alone.
// A counter never repeats under one session key, and every agent process has
// its own session key.
func (k *FrameKeys) OwnNonce() ([]byte, error) {
	n := binary.BigEndian.AppendUint64(make([]byte, 0, 12), k.ownCtr.Add(1))
	return append(n, k.markOf(n)...), nil
}

// IsOwnNonce reports whether nonce came from OwnNonce under this key.
func (k *FrameKeys) IsOwnNonce(nonce []byte) bool {
	return len(nonce) == 12 && hmac.Equal(nonce[8:], k.markOf(nonce[:8]))
}

func (k *FrameKeys) markOf(b []byte) []byte {
	m := hmac.New(sha256.New, k.ownMark)
	m.Write(b)
	return m.Sum(nil)[:4]
}

// FrameAAD is the associated data of one frame. Exported for the
// cross-language test vectors.
func FrameAAD(dir byte, inner string, stream uint32, ctr, seq uint64) []byte {
	b := make([]byte, 0, len(frameAADTag)+1+4+len(inner)+4+8+8)
	b = append(b, frameAADTag...)
	b = append(b, dir)
	b = binary.BigEndian.AppendUint32(b, uint32(len(inner)))
	b = append(b, inner...)
	b = binary.BigEndian.AppendUint32(b, stream)
	b = binary.BigEndian.AppendUint64(b, ctr)
	b = binary.BigEndian.AppendUint64(b, seq)
	return b
}

func (k *FrameKeys) aead(dir byte) (cipher.AEAD, error) {
	switch dir {
	case FromAgent:
		return k.a2v, nil
	case FromViewer:
		return k.v2a, nil
	}
	return nil, errors.New("unknown frame direction")
}

// What a frame's payload was before sealing, so the receiver can rebuild the
// message exactly: no payload, a payload that was encrypted with the session
// key (sealed as its plaintext), or a payload that was sent as it is.
const (
	FrameEmpty byte = 0
	FrameBoxed byte = 1
	FrameRaw   byte = 2
)

// Seal encrypts one frame.
func (k *FrameKeys) Seal(dir byte, inner string, stream uint32, ctr, seq uint64, kind byte, payload []byte) (string, error) {
	a, err := k.aead(dir)
	if err != nil {
		return "", err
	}
	if kind > FrameRaw {
		return "", errors.New("unknown frame kind")
	}
	pt := make([]byte, 0, 1+len(payload))
	pt = append(pt, kind)
	pt = append(pt, payload...)
	nonce := make([]byte, a.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := a.Seal(nonce, nonce, pt, FrameAAD(dir, inner, stream, ctr, seq))
	return base64.StdEncoding.EncodeToString(out), nil
}

// Open decrypts one frame sealed with the same direction, type, stream,
// counter and seq.
func (k *FrameKeys) Open(dir byte, inner string, stream uint32, ctr, seq uint64, data string) (kind byte, payload []byte, err error) {
	a, err := k.aead(dir)
	if err != nil {
		return 0, nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil || len(raw) < a.NonceSize() {
		return 0, nil, errors.New("malformed frame")
	}
	pt, err := a.Open(nil, raw[:a.NonceSize()], raw[a.NonceSize():], FrameAAD(dir, inner, stream, ctr, seq))
	if err != nil || len(pt) == 0 || pt[0] > FrameRaw {
		return 0, nil, errors.New("frame does not open")
	}
	return pt[0], pt[1:], nil
}

// SealInfo is what a handshake answer tells a viewer that can use sealed
// frames: the stream to send on, and the agent counter it has already passed.
type SealInfo struct {
	Stream  uint32
	AgentAt uint64
}

func sealInfoAEAD(shared, exID []byte) (cipher.AEAD, error) {
	k := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, shared, exID, sealInfoInfo), k); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// SealSealInfo encrypts info under the handshake's shared secret.
func SealSealInfo(shared, exID []byte, info SealInfo) (string, error) {
	a, err := sealInfoAEAD(shared, exID)
	if err != nil {
		return "", err
	}
	pt := binary.BigEndian.AppendUint32(nil, info.Stream)
	pt = binary.BigEndian.AppendUint64(pt, info.AgentAt)
	nonce := make([]byte, a.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(a.Seal(nonce, nonce, pt, exID)), nil
}

// OpenSealInfo reverses SealSealInfo.
func OpenSealInfo(shared, exID []byte, sealed string) (SealInfo, error) {
	a, err := sealInfoAEAD(shared, exID)
	if err != nil {
		return SealInfo{}, err
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil || len(raw) < a.NonceSize() {
		return SealInfo{}, errors.New("malformed seal info")
	}
	pt, err := a.Open(nil, raw[:a.NonceSize()], raw[a.NonceSize():], exID)
	if err != nil || len(pt) != 12 {
		return SealInfo{}, errors.New("seal info does not open")
	}
	return SealInfo{Stream: binary.BigEndian.Uint32(pt), AgentAt: binary.BigEndian.Uint64(pt[4:])}, nil
}
