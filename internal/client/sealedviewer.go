// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"encoding/json"
	"errors"

	"reminal/internal/crypto"
	"reminal/internal/protocol"
)

// Sealed frames, client side (see crypto/frames.go and sealedframes.go). Used
// by the terminal viewer and by the machine-channel client
// (directoryquery.go).

// viewerSeal is one connection's sealed-frame state. Every connection runs its
// own handshake, so it starts empty each time.
type viewerSeal struct {
	keys   *crypto.FrameKeys // nil: the agent predates sealed frames
	stream uint32
	outCtr uint64
	inLast uint64
}

// errSealedDowngrade: the agent sends sealed frames, but this connection's
// handshake answer said it does not; the viewer reconnects.
var errSealedDowngrade = errors.New("the connection to that machine was interrupted — reconnecting")

// newViewerSeal reads what a handshake answer said about sealed frames.
func newViewerSeal(sessionKey, key, exID []byte, sealed string) (viewerSeal, error) {
	if sealed == "" {
		return viewerSeal{}, nil
	}
	info, err := crypto.OpenSealInfo(key, exID, sealed)
	if err != nil {
		return viewerSeal{}, errors.New("handshake: the machine's answer did not check out")
	}
	keys, err := crypto.NewFrameKeys(sessionKey)
	if err != nil {
		return viewerSeal{}, err
	}
	return viewerSeal{keys: keys, stream: info.Stream, inLast: info.AgentAt}, nil
}

// out returns the sealed encoding of msg, or nil when it goes as it is. box
// is the session key's box the message's Data was encrypted with.
func (s *viewerSeal) out(box *crypto.Box, msg protocol.Message) ([]byte, error) {
	if s.keys == nil || !protocol.Sealable(msg.Type) {
		return nil, nil
	}
	kind, payload := crypto.FrameEmpty, []byte(nil)
	if msg.Data != "" {
		if pt, err := box.Decrypt(msg.Data); err == nil {
			kind, payload = crypto.FrameBoxed, pt
		} else {
			kind, payload = crypto.FrameRaw, []byte(msg.Data)
		}
	}
	s.outCtr++
	data, err := s.keys.Seal(crypto.FromViewer, string(msg.Type), s.stream, s.outCtr, msg.Seq, kind, payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(protocol.Message{Type: protocol.TypeSealed, Inner: msg.Type, Stream: s.stream, Ctr: s.outCtr, Seq: msg.Seq, Data: data})
}

// in decides whether an incoming message is acted on, unwrapping a sealed
// one; a payload comes back encrypted with box, as the handlers expect.
func (s *viewerSeal) in(box *crypto.Box, msg protocol.Message) (protocol.Message, bool, error) {
	if msg.Type == protocol.TypeSealed {
		if s.keys == nil {
			return msg, false, errSealedDowngrade
		}
		if !protocol.Sealable(msg.Inner) || msg.Ctr <= s.inLast {
			return msg, false, nil
		}
		kind, payload, err := s.keys.Open(crypto.FromAgent, string(msg.Inner), 0, msg.Ctr, msg.Seq, msg.Data)
		if err != nil {
			return msg, false, nil
		}
		s.inLast = msg.Ctr
		out := protocol.Message{Type: msg.Inner, Seq: msg.Seq}
		switch kind {
		case crypto.FrameBoxed:
			enc, err := box.Encrypt(payload)
			if err != nil {
				return msg, false, nil
			}
			out.Data = enc
		case crypto.FrameRaw:
			out.Data = string(payload)
		}
		return out, true, nil
	}
	// The agent writes an unsealed copy for clients that predate sealed
	// frames; one that seals takes only the sealed one.
	if s.keys != nil && protocol.Sealable(msg.Type) {
		return msg, false, nil
	}
	return msg, true, nil
}

func (v *Viewer) resetSeal() {
	v.writeMu.Lock()
	v.seal = viewerSeal{}
	v.writeMu.Unlock()
}

// setSeal records what a handshake answer said about sealed frames.
func (v *Viewer) setSeal(sessionKey, key, exID []byte, sealed string) error {
	st, err := newViewerSeal(sessionKey, key, exID, sealed)
	v.writeMu.Lock()
	v.seal = st
	v.writeMu.Unlock()
	return err
}

// sealOut is viewerSeal.out for this viewer. Called with writeMu held.
func (v *Viewer) sealOut(msg protocol.Message) ([]byte, error) { return v.seal.out(v.box, msg) }

// admitIn is viewerSeal.in for this viewer. Only the reader calls it.
func (v *Viewer) admitIn(msg protocol.Message) (protocol.Message, bool, error) {
	return v.seal.in(v.box, msg)
}
