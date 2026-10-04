// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"crypto/sha256"
	"encoding/json"
	"log"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"

	"reminal/internal/crypto"
	"reminal/internal/protocol"
)

// Sealed frames, agent side (see crypto/frames.go for the format).
//
// Outgoing: every session message is written sealed. While a viewer that
// predates sealed frames is attached, it is also written as before, so that
// viewer still sees the session.
//
// Incoming: a sealed frame is accepted once, from a stream a handshake issued,
// in order, as the message type it was sealed as. An unsealed message is a
// viewer that predates sealed frames: it is accepted only if it decrypts with
// the session key and is not one the agent wrote itself, and it puts the
// session into writing both forms until the last viewer leaves.

// maxStreams bounds how many handshakes' streams are remembered. Past it the
// oldest is forgotten, and a frame on it is refused, never accepted afresh.
const maxStreams = 4096

// legacyEchoes bounds how many of the agent's own unsealed messages are
// remembered (isEcho).
const legacyEchoes = 1 << 15

type sealState struct {
	once sync.Once
	keys *crypto.FrameKeys

	outCtr uint64 // under Agent.writeMu

	mu         sync.Mutex
	streams    map[uint32]uint64 // stream → last counter accepted
	order      []uint32
	nextStream uint32

	legacy atomic.Bool // a viewer that predates sealed frames is attached

	echoMu  sync.Mutex
	echoes  map[[16]byte]struct{}
	echoLog [][16]byte
	echoPos int
}

// frameKeys returns this agent's frame keys (a session's, or the machine
// channel's), or nil for an agent without a session key.
func (a *Agent) frameKeys() *crypto.FrameKeys {
	if len(a.sessionKey) != 32 {
		return nil
	}
	a.seal.once.Do(func() {
		k, err := crypto.NewFrameKeys(a.sessionKey)
		if err != nil {
			log.Printf("sealed frames off: %v", err)
			return
		}
		a.seal.keys = k
		a.seal.streams = map[uint32]uint64{}
		a.seal.echoes = map[[16]byte]struct{}{}
	})
	return a.seal.keys
}

// sealInfoFor issues a stream to a viewer that asked for sealed frames and
// returns its encrypted SealInfo for the handshake answer; "" otherwise.
func (a *Agent) sealInfoFor(frames int, key, exID []byte) string {
	k := a.frameKeys()
	if k == nil || frames < 1 {
		return ""
	}
	a.seal.mu.Lock()
	a.seal.nextStream++
	stream := a.seal.nextStream
	a.seal.streams[stream] = 0
	a.seal.order = append(a.seal.order, stream)
	if len(a.seal.order) > maxStreams {
		delete(a.seal.streams, a.seal.order[0])
		a.seal.order = a.seal.order[1:]
	}
	a.seal.mu.Unlock()
	a.writeMu.Lock()
	at := a.seal.outCtr
	a.writeMu.Unlock()
	info, err := crypto.SealSealInfo(key, exID, crypto.SealInfo{Stream: stream, AgentAt: at})
	if err != nil {
		return ""
	}
	return info
}

// sealedFrame builds the sealed form of msg with counter ctr. Called with
// writeMu held, so counters go out in the order they are given.
func (a *Agent) sealedFrame(k *crypto.FrameKeys, msg protocol.Message, ctr uint64) ([]byte, error) {
	kind, payload := crypto.FrameEmpty, []byte(nil)
	if msg.Data != "" {
		if pt, err := a.box.Decrypt(msg.Data); err == nil {
			kind, payload = crypto.FrameBoxed, pt
		} else {
			kind, payload = crypto.FrameRaw, []byte(msg.Data)
		}
	}
	data, err := k.Seal(crypto.FromAgent, string(msg.Type), 0, ctr, msg.Seq, kind, payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(protocol.Message{Type: protocol.TypeSealed, Inner: msg.Type, Ctr: ctr, Seq: msg.Seq, Data: data})
}

// writeSessionMsg writes one sealable message: sealed, and as it is too while
// a viewer that predates sealed frames is attached.
func (a *Agent) writeSessionMsg(conn *websocket.Conn, k *crypto.FrameKeys, msg protocol.Message) error {
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	a.seal.outCtr++
	sealed, err := a.sealedFrame(k, msg, a.seal.outCtr)
	if err != nil {
		return err
	}
	if err := a.writeRaw(conn, sealed); err != nil {
		return err
	}
	if !a.seal.legacy.Load() {
		return nil
	}
	plain, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if msg.Data != "" {
		a.rememberEcho(msg.Data)
	}
	return a.writeRaw(conn, plain)
}

func echoKey(data string) [16]byte {
	sum := sha256.Sum256([]byte(data))
	var k [16]byte
	copy(k[:], sum[:])
	return k
}

func (a *Agent) rememberEcho(data string) {
	k := echoKey(data)
	a.seal.echoMu.Lock()
	defer a.seal.echoMu.Unlock()
	if len(a.seal.echoLog) < legacyEchoes {
		a.seal.echoLog = append(a.seal.echoLog, k)
	} else {
		delete(a.seal.echoes, a.seal.echoLog[a.seal.echoPos])
		a.seal.echoLog[a.seal.echoPos] = k
		a.seal.echoPos = (a.seal.echoPos + 1) % legacyEchoes
	}
	a.seal.echoes[k] = struct{}{}
}

func (a *Agent) isEcho(data string) bool {
	a.seal.echoMu.Lock()
	defer a.seal.echoMu.Unlock()
	_, ok := a.seal.echoes[echoKey(data)]
	return ok
}

// admit decides whether an incoming message is acted on, and unwraps a
// sealed one into the message it carries. legacyNow reports that this message
// is the first from a viewer that predates sealed frames.
func (a *Agent) admit(msg protocol.Message) (out protocol.Message, ok, legacyNow bool) {
	k := a.frameKeys()
	if k == nil {
		return msg, msg.Type != protocol.TypeSealed, false
	}
	if msg.Type == protocol.TypeSealed {
		m, ok := a.openSealed(k, msg)
		return m, ok, false
	}
	if !protocol.Sealable(msg.Type) {
		return msg, true, false
	}
	// Unsealed session message, from a viewer that predates sealed frames.
	// One that does not decrypt, or that the agent wrote itself, is dropped.
	if msg.Data == "" {
		// A bare request for something to be sent back is how an older
		// client asks for a list or a status, and changes nothing; it is
		// answered, in the form that client reads. Anything else bare is
		// accepted only while such a client has shown itself.
		if protocol.ReadOnlyRequest(msg.Type) {
			return msg, true, !a.seal.legacy.Swap(true)
		}
		return msg, a.seal.legacy.Load(), false
	}
	if _, err := a.box.Decrypt(msg.Data); err != nil || a.isEcho(msg.Data) {
		return msg, false, false
	}
	return msg, true, !a.seal.legacy.Swap(true)
}

// openSealed checks a viewer's sealed frame against its stream and opens it.
func (a *Agent) openSealed(k *crypto.FrameKeys, msg protocol.Message) (protocol.Message, bool) {
	if !protocol.Sealable(msg.Inner) {
		return msg, false
	}
	a.seal.mu.Lock()
	last, known := a.seal.streams[msg.Stream]
	a.seal.mu.Unlock()
	if !known || msg.Ctr <= last {
		return msg, false
	}
	kind, payload, err := k.Open(crypto.FromViewer, string(msg.Inner), msg.Stream, msg.Ctr, msg.Seq, msg.Data)
	if err != nil {
		return msg, false
	}
	a.seal.mu.Lock()
	last, known = a.seal.streams[msg.Stream]
	if !known || msg.Ctr <= last {
		a.seal.mu.Unlock()
		return msg, false
	}
	a.seal.streams[msg.Stream] = msg.Ctr
	a.seal.mu.Unlock()
	out := protocol.Message{Type: msg.Inner, Seq: msg.Seq}
	switch kind {
	case crypto.FrameBoxed:
		// The handlers decrypt with the session key; hand them what they expect.
		enc, err := a.box.Encrypt(payload)
		if err != nil {
			return msg, false
		}
		out.Data = enc
	case crypto.FrameRaw:
		out.Data = string(payload)
	}
	return out, true
}

// lastViewerLeft ends writing both forms: the viewers that needed the
// unsealed one are gone, and the next one says which it needs.
func (a *Agent) lastViewerLeft() { a.seal.legacy.Store(false) }
