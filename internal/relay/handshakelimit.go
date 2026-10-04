// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package relay

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"reminal/internal/protocol"
)

// PIN handshakes are paced per session and address: at most handshakeBurst
// from one address, then one per handshakeRefill, counted only while the
// session's machine is connected. A paced handshake is answered with
// TypePakeBusy (try again shortly), which a viewer that predates it ignores.
// Each forwarded handshake carries an opaque tag for its source (Message.Src)
// so the machine can keep a share of its own allowance per source. IPv6
// addresses are grouped by /64. Same numbers as the Worker
// (cloudflare/src/session.ts).
const (
	handshakeBurst  = 10
	handshakeRefill = time.Minute
)

type addrBucket struct {
	tokens float64
	at     time.Time
}

// trustProxyHeaders, set by REMINAL_RELAY_TRUST_PROXY=1, takes a viewer's
// address from X-Real-IP, or else the last X-Forwarded-For entry. Only for a
// relay behind a reverse proxy that sets them; otherwise every viewer would
// share the proxy's address.
var trustProxyHeaders = os.Getenv("REMINAL_RELAY_TRUST_PROXY") == "1"

// clientAddrs holds each connection's client address, recorded at upgrade.
var clientAddrs sync.Map // *websocket.Conn → string

var tagSalt = func() []byte {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return b
}()

// noteClientAddr records conn's client address from its upgrade request.
func noteClientAddr(conn *websocket.Conn, r *http.Request) {
	addr := r.RemoteAddr
	if host, _, err := net.SplitHostPort(addr); err == nil {
		addr = host
	}
	if trustProxyHeaders {
		// X-Real-IP is set by the proxy. X-Forwarded-For is appended to, so
		// only its last entry is the proxy's own; anything before it came
		// from the client.
		if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
			addr = xr
		} else if lines := r.Header.Values("X-Forwarded-For"); len(lines) > 0 {
			// The proxy's entry is the last one, on the last line.
			parts := strings.Split(lines[len(lines)-1], ",")
			addr = strings.TrimSpace(parts[len(parts)-1])
		}
	}
	clientAddrs.Store(conn, addr)
}

func forgetClientAddr(conn *websocket.Conn) { clientAddrs.Delete(conn) }

// addressGroup is the unit handshakes are counted by: the address, or its /64
// for IPv6.
func addressGroup(addr string) string {
	ip := net.ParseIP(addr)
	if ip == nil {
		return addr
	}
	if ip.To4() != nil {
		return ip.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

func sourceTag(group string) string {
	h := sha256.Sum256(append(append([]byte(nil), tagSalt...), group...))
	return hex.EncodeToString(h[:12])
}

// admitHandshake decides whether a viewer's PIN handshake goes on to the
// agent, tagging it with its source. retry is how long to wait when it does
// not. Anything else passes untouched.
func (s *Server) admitHandshake(sessionID string, role protocol.Role, conn *websocket.Conn, msg *protocol.Message) (ok bool, retry time.Duration) {
	if role != protocol.RoleViewer || (msg.Type != protocol.TypePakeInit && msg.Type != protocol.TypeKexInit) {
		return true, 0
	}
	addr, _ := clientAddrs.Load(conn)
	a, _ := addr.(string)
	if a == "" {
		a = remoteHost(conn)
	}
	group := addressGroup(a)
	msg.Src = sourceTag(group)
	if !s.agentConnected(sessionID) {
		return true, 0 // nobody to spend an allowance on; the agent is away
	}
	return s.takeHandshake(sessionID+"|"+group, time.Now())
}

func (s *Server) agentConnected(sessionID string) bool {
	r := s.getRoom(sessionID)
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.agent != nil && r.agent.authed // what forward() delivers to
}

func (s *Server) takeHandshake(key string, now time.Time) (bool, time.Duration) {
	s.hsMu.Lock()
	defer s.hsMu.Unlock()
	if s.handshakes == nil {
		s.handshakes = make(map[string]*addrBucket)
	}
	b, ok := s.handshakes[key]
	if !ok {
		// A full bucket is the same as no entry, so once the table is large
		// drop the ones that have refilled.
		if len(s.handshakes) > 4096 {
			for k, o := range s.handshakes {
				if now.Sub(o.at) >= handshakeBurst*handshakeRefill {
					delete(s.handshakes, k)
				}
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
		return false, time.Duration((1 - b.tokens) * float64(handshakeRefill))
	}
	b.tokens--
	return true, 0
}

// pakeBusy is the answer to a paced handshake.
func pakeBusy(msg protocol.Message, retry time.Duration) protocol.Message {
	return protocol.Message{Type: protocol.TypePakeBusy, ExID: msg.ExID, RetryMS: int(retry / time.Millisecond)}
}

func remoteHost(conn *websocket.Conn) string {
	addr := conn.RemoteAddr().String()
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}
