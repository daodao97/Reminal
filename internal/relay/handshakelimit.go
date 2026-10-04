// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package relay

import (
	"net"
	"time"

	"github.com/gorilla/websocket"

	"reminal/internal/protocol"
)

// PIN handshakes are paced per session and address: at most handshakeBurst
// from one address, then one per handshakeRefill, a share of the machine's own
// allowance (kexBurst and kexLongBurst in internal/client) and more than a
// person connecting and reconnecting needs. Same numbers as the Worker
// (cloudflare/src/session.ts).
const (
	handshakeBurst  = 6
	handshakeRefill = 10 * time.Minute
)

const tooManyHandshakes = "too many connection attempts from your network — try again in a few minutes"

type addrBucket struct {
	tokens float64
	at     time.Time
}

// handshakeAllowed reports whether msg may go on to the agent. Only a viewer's
// PIN handshake is counted.
func (s *Server) handshakeAllowed(sessionID string, role protocol.Role, conn *websocket.Conn, msg protocol.Message) bool {
	if role != protocol.RoleViewer || (msg.Type != protocol.TypePakeInit && msg.Type != protocol.TypeKexInit) {
		return true
	}
	return s.takeHandshake(sessionID+"|"+remoteHost(conn), time.Now())
}

func (s *Server) takeHandshake(key string, now time.Time) bool {
	s.hsMu.Lock()
	defer s.hsMu.Unlock()
	if s.handshakes == nil {
		s.handshakes = make(map[string]*addrBucket)
	}
	b, ok := s.handshakes[key]
	if !ok {
		// A full bucket is the same as no entry, so drop the ones that have
		// refilled while making room for this one.
		for k, o := range s.handshakes {
			if now.Sub(o.at) >= handshakeBurst*handshakeRefill {
				delete(s.handshakes, k)
			}
		}
		b = &addrBucket{tokens: handshakeBurst, at: now}
		s.handshakes[key] = b
	}
	b.tokens += float64(now.Sub(b.at)) / float64(handshakeRefill)
	if b.tokens > handshakeBurst {
		b.tokens = handshakeBurst
	}
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func remoteHost(conn *websocket.Conn) string {
	addr := conn.RemoteAddr().String()
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}
