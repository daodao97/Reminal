// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package relay

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestHandshakesPerAddress(t *testing.T) {
	s := NewServer()
	now := time.Now()
	for i := 0; i < handshakeBurst; i++ {
		if ok, _ := s.takeHandshake("SESS|10.0.0.1", now); !ok {
			t.Fatalf("handshake %d from one address refused within the burst", i+1)
		}
	}
	ok, retry := s.takeHandshake("SESS|10.0.0.1", now)
	if ok || retry <= 0 || retry > handshakeRefill {
		t.Fatalf("past the burst: ok=%v retry=%v", ok, retry)
	}
	// Another address, and the same address on another session, are separate.
	if ok, _ := s.takeHandshake("SESS|10.0.0.2", now); !ok {
		t.Fatal("one address's handshakes counted against another")
	}
	if ok, _ := s.takeHandshake("OTHER|10.0.0.1", now); !ok {
		t.Fatal("one session's handshakes counted against another")
	}
	if ok, _ := s.takeHandshake("SESS|10.0.0.1", now.Add(handshakeRefill)); !ok {
		t.Fatal("no handshake allowed after a refill interval")
	}
}

func TestAddressGroup(t *testing.T) {
	cases := map[string]string{
		"203.0.113.7":             "203.0.113.7",
		"2001:db8:1:2:aaaa::1":    "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff::9999": "2001:db8:1:2::/64",
		"not-an-address":          "not-an-address",
	}
	for in, want := range cases {
		if got := addressGroup(in); got != want {
			t.Errorf("addressGroup(%q) = %q, want %q", in, got, want)
		}
	}
	if sourceTag("203.0.113.7") == sourceTag("203.0.113.8") {
		t.Error("two addresses share a source tag")
	}
}

func TestProxyHeaderAddress(t *testing.T) {
	prev := trustProxyHeaders
	trustProxyHeaders = true
	t.Cleanup(func() { trustProxyHeaders = prev })
	addr := func(h map[string]string) string {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "10.0.0.5:1234"
		for k, v := range h {
			r.Header.Set(k, v)
		}
		c := &websocket.Conn{}
		noteClientAddr(c, r)
		v, _ := clientAddrs.Load(c)
		forgetClientAddr(c)
		return v.(string)
	}
	if got := addr(map[string]string{"X-Forwarded-For": "6.6.6.6, 198.51.100.9", "X-Real-IP": "198.51.100.9"}); got != "198.51.100.9" {
		t.Errorf("with X-Real-IP: %s", got)
	}
	if got := addr(map[string]string{"X-Forwarded-For": "6.6.6.6, 198.51.100.9"}); got != "198.51.100.9" {
		t.Errorf("X-Forwarded-For: %s, want the last entry", got)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:1234"
	r.Header.Add("X-Forwarded-For", "6.6.6.6")
	r.Header.Add("X-Forwarded-For", "198.51.100.9")
	c := &websocket.Conn{}
	noteClientAddr(c, r)
	if v, _ := clientAddrs.Load(c); v != "198.51.100.9" {
		t.Errorf("two X-Forwarded-For lines: %v, want the last line's entry", v)
	}
	forgetClientAddr(c)
	if got := addr(nil); got != "10.0.0.5" {
		t.Errorf("no headers: %s", got)
	}
}
