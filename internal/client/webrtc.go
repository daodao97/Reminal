// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
	"github.com/reminal/reminal/internal/protocol"
)

// rtcProbeWindow is how long a connected-but-unconfirmed DataChannel is probed
// (frames sent over both it and WS) before we give up on it. A channel that
// hasn't delivered a single frame in this long — the cellular-MTU case — is
// closed so we stop wasting sends on it; the viewer re-negotiates later.
const rtcProbeWindow = 8 * time.Second

// rtcHandshakeTimeout bounds how long a freshly-offered peer may sit before its
// DataChannel opens. A viewer that requests an offer but never answers (crashed
// tab, flaky mobile, or a viewer spamming hellos with distinct peer ids) leaves
// a PeerConnection stuck in "new" — pion never fires Failed without a remote
// description, and rtcSinks only reaps peers that DID open — so nothing else
// collects it until the last viewer leaves. This reaps the un-opened peer.
const rtcHandshakeTimeout = 30 * time.Second

// Window frames are the relay's heaviest traffic, and the Cloudflare relay
// bills every forwarded WebSocket message as a request. When a viewer can open
// a WebRTC DataChannel, frames (and their acks) flow directly peer-to-peer over
// DTLS instead, taking the relay out of the hot path entirely — the relay only
// carries the handshake (a few messages) and stays the reliable fallback if the
// peer connection never forms.
//
// Trust: the SDP offer/answer and ICE candidates ride end-to-end encrypted in
// the existing session channel (sealed under the PIN-authenticated key), so the
// relay can't tamper with the DTLS fingerprints and therefore can't MITM the
// connection. Frames on the DataChannel are DTLS-protected, so they don't need
// the app-layer AES-GCM the WS path uses.

// STUN discovers each peer's public address so most connections go direct; a
// TURN server relays the (still DTLS-encrypted) media for peers that can't punch
// through — cellular CGNAT, strict firewalls. TURN is opt-in so no credentials
// live in the public frontend; the agent hands its ICE config to the viewer over
// the encrypted signaling channel (see iceConfig), so credentials never touch
// the served HTML. Configure EITHER:
//   - Cloudflare TURN (ephemeral creds): REMINAL_TURN_CF_KEY + REMINAL_TURN_CF_TOKEN
//   - a static server: REMINAL_TURN (comma-separated urls) + REMINAL_TURN_USER/_PASS
// With neither we're STUN-only and un-punchable peers stay on the WS relay.

// iceServerJSON is the wire form of one ICE server — it matches both the
// browser's RTCIceServer and Cloudflare's credential-API response, so servers
// round-trip straight through to the viewer.
type iceServerJSON struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// iceConfig returns the ICE servers for a new connection in both the agent's
// (pion) and the viewer's (JSON) form, from one source so both peers use the
// same servers.
func iceConfig() ([]webrtc.ICEServer, []iceServerJSON) {
	js := iceServersJSON()
	pion := make([]webrtc.ICEServer, 0, len(js))
	for _, s := range js {
		ice := webrtc.ICEServer{URLs: s.URLs}
		if s.Username != "" {
			ice.Username = s.Username
			ice.Credential = s.Credential
			ice.CredentialType = webrtc.ICECredentialTypePassword
		}
		pion = append(pion, ice)
	}
	return pion, js
}

func iceServersJSON() []iceServerJSON {
	if cf, ok := cloudflareICE(); ok {
		return cf
	}
	stun := iceServerJSON{URLs: []string{"stun:stun.l.google.com:19302"}}
	var urls []string
	for _, u := range strings.Split(os.Getenv("REMINAL_TURN"), ",") {
		if u = strings.TrimSpace(u); u != "" {
			urls = append(urls, u)
		}
	}
	if len(urls) == 0 {
		return []iceServerJSON{stun}
	}
	return []iceServerJSON{stun, {URLs: urls, Username: os.Getenv("REMINAL_TURN_USER"), Credential: os.Getenv("REMINAL_TURN_PASS")}}
}

// cloudflareICE mints short-lived TURN credentials from Cloudflare's TURN
// credential API, caching them until half their TTL elapses so we don't hit the
// API on every connection. Returns false (→ STUN/static fallback) if it isn't
// configured or the call fails.
var (
	cfMu      sync.Mutex
	cfCache   []iceServerJSON
	cfExpires time.Time
)

func cloudflareICE() ([]iceServerJSON, bool) {
	keyID := os.Getenv("REMINAL_TURN_CF_KEY")
	token := os.Getenv("REMINAL_TURN_CF_TOKEN")
	if keyID == "" || token == "" {
		return nil, false
	}
	cfMu.Lock()
	defer cfMu.Unlock()
	if cfCache != nil && time.Now().Before(cfExpires) {
		return cfCache, true
	}
	const ttl = 86400 // seconds
	req, err := http.NewRequest(http.MethodPost,
		"https://rtc.live.cloudflare.com/v1/turn/keys/"+keyID+"/credentials/generate-ice-servers",
		strings.NewReader(`{"ttl":86400}`))
	if err != nil {
		return nil, false
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, false
	}
	var out struct {
		ICEServers []iceServerJSON `json:"iceServers"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil || len(out.ICEServers) == 0 {
		return nil, false
	}
	cfCache = out.ICEServers
	cfExpires = time.Now().Add((ttl / 2) * time.Second)
	return cfCache, true
}

// rtcPeer is one viewer's peer connection and its frame DataChannel.
type rtcPeer struct {
	id        string
	pc        *webrtc.PeerConnection
	dc        *webrtc.DataChannel
	h264      bool        // viewer declared WebCodecs H.264 decode in its hello
	open      atomic.Bool // true once the DataChannel is ready to carry frames
	confirmed atomic.Bool // true once the viewer acked a frame OVER this channel
	// (proving big frames actually traverse it — small acks alone can't)

	mu         sync.Mutex
	openedAt   time.Time                 // when probing started (guarded by mu)
	haveRemote bool                      // remote description applied yet?
	pendingICE []webrtc.ICECandidateInit // candidates that arrived early
}

func (p *rtcPeer) startProbe() {
	p.mu.Lock()
	p.openedAt = time.Now()
	p.mu.Unlock()
}
func (p *rtcPeer) probeAge() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return time.Since(p.openedAt)
}

// signal payloads (the JSON inside each webrtc_* message's encrypted Data).
type rtcHelloMsg struct {
	Peer string `json:"peer"`
	// H264 declares the viewer can decode H.264 via WebCodecs, so frames on
	// this peer's DataChannel may be compressed video instead of JPEGs (7-10×
	// less bandwidth at the same quality). Old viewers omit it → JPEG forever.
	H264 bool `json:"h264,omitempty"`
}
type rtcSDPMsg struct {
	Peer string          `json:"peer"`
	SDP  string          `json:"sdp"`
	ICE  []iceServerJSON `json:"ice,omitempty"` // agent→viewer on the offer only
}
type rtcICEMsg struct {
	Peer      string  `json:"peer"`
	Candidate string  `json:"candidate"`
	Mid       *string `json:"mid,omitempty"`
	Line      *uint16 `json:"line,omitempty"`
}

// handleWebRTCHello answers a viewer's offer request: build a PeerConnection
// with a frames DataChannel, create an offer, and send it back. ICE candidates
// are trickled as they're gathered.
func (a *Agent) handleWebRTCHello(conn *websocket.Conn, encData string) {
	// Runs in its own goroutine (go a.handleWebRTCHello), so a panic here escapes
	// runConnection's recover and would crash the agent. Contain it: a malformed
	// viewer hello must at worst drop this P2P attempt (WS fallback covers it).
	defer func() { _ = recover() }()
	plaintext, err := a.box.Decrypt(encData)
	if err != nil {
		return
	}
	var hello rtcHelloMsg
	if json.Unmarshal(plaintext, &hello) != nil || hello.Peer == "" {
		return
	}

	// Record the viewer's decode capability BEFORE anything can fail. A viewer
	// whose DataChannel never opens still reaches us over the relay, and it
	// still wants video — so capability tracking must not be tied to P2P
	// succeeding. Viewers re-hello periodically while unconnected, which keeps
	// this fresh (see viewerCapTTL).
	a.noteViewerCap(hello.Peer, hello.H264)

	pionICE, viewerICE := iceConfig()
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{ICEServers: pionICE})
	if err != nil {
		return // frames stay on the WS fallback
	}
	peer := &rtcPeer{id: hello.Peer, pc: pc, h264: hello.H264}

	// Reliable, ordered channel — same delivery guarantees as the WS path, so
	// the viewer's render/ack loop is unchanged.
	dc, err := pc.CreateDataChannel("frames", nil)
	if err != nil {
		_ = pc.Close()
		return
	}
	peer.dc = dc
	dc.OnOpen(func() { peer.startProbe(); peer.open.Store(true) })
	dc.OnClose(func() { peer.open.Store(false) })
	dc.OnMessage(func(m webrtc.DataChannelMessage) {
		// Runs in pion's read goroutine, outside every other recover, on
		// viewer-supplied data. Contain any panic so a crafted DataChannel message
		// can't crash the agent.
		defer func() {
			if r := recover(); r != nil {
				recoverLog("dc.OnMessage", r)
			}
		}()
		// The only viewer→agent traffic on this channel is frame acks. Because
		// the viewer acks a frame over the SAME transport it arrived on, an ack
		// here proves a full frame really traversed this channel — so it's now
		// safe to send frames over it exclusively.
		peer.confirmed.Store(true)
		var ack struct {
			ID  string `json:"id"`
			Seq uint64 `json:"seq"`
		}
		if json.Unmarshal(m.Data, &ack) == nil && ack.ID != "" {
			a.deliverWindowAck(ack.ID, ack.Seq)
		}
	})

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return // nil marks end-of-candidates
		}
		ci := c.ToJSON()
		a.sendWindowMsg(a.liveConn(), protocol.TypeWebRTCICE, rtcICEMsg{
			Peer: hello.Peer, Candidate: ci.Candidate, Mid: ci.SDPMid, Line: ci.SDPMLineIndex,
		})
	})
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		// Tear down ONLY on terminal states. "Disconnected" is transient — ICE
		// keepalives missed for a few seconds, routine on phones (WiFi power-
		// save, brief radio fades) — and normally self-heals back to
		// "connected"; killing the peer on it made every radio nap a full
		// relay-fallback + renegotiate cycle, flapping the transport with no
		// user-visible network change. While disconnected, the stream's own
		// liveness machinery covers us: acks dry up → frames demote to WS in
		// ~6s; the channel re-proves via a probe when connectivity returns; and
		// a blip that doesn't heal escalates to "failed", which lands here.
		if s == webrtc.PeerConnectionStateFailed || s == webrtc.PeerConnectionStateClosed {
			peer.open.Store(false)
			a.dropRTCPeer(hello.Peer, peer)
		}
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		_ = pc.Close()
		return
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		_ = pc.Close()
		return
	}

	a.rtcMu.Lock()
	if a.rtcPeers == nil {
		a.rtcPeers = map[string]*rtcPeer{}
	}
	// Replace any prior connection for this peer id (viewer reconnected).
	if old := a.rtcPeers[hello.Peer]; old != nil {
		_ = old.pc.Close()
	}
	a.rtcPeers[hello.Peer] = peer
	a.rtcMu.Unlock()

	// Reap the peer if its DataChannel never opens (viewer never answered). Fire-
	// and-forget: if it opened, open.Load() is true and this no-ops; dropRTCPeer's
	// identity guard handles the case where a reconnect already replaced it.
	time.AfterFunc(rtcHandshakeTimeout, func() {
		if !peer.open.Load() {
			a.dropRTCPeer(hello.Peer, peer)
		}
	})

	a.sendWindowMsg(conn, protocol.TypeWebRTCOffer, rtcSDPMsg{Peer: hello.Peer, SDP: offer.SDP, ICE: viewerICE})
}

// handleWebRTCAnswer applies the viewer's answer to the matching connection and
// flushes any ICE candidates that arrived before it.
func (a *Agent) handleWebRTCAnswer(encData string) {
	plaintext, err := a.box.Decrypt(encData)
	if err != nil {
		return
	}
	var ans rtcSDPMsg
	if json.Unmarshal(plaintext, &ans) != nil || ans.Peer == "" {
		return
	}
	peer := a.rtcPeerByID(ans.Peer)
	if peer == nil {
		return
	}
	if err := peer.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer, SDP: ans.SDP,
	}); err != nil {
		return
	}
	peer.mu.Lock()
	peer.haveRemote = true
	pending := peer.pendingICE
	peer.pendingICE = nil
	peer.mu.Unlock()
	for _, c := range pending {
		_ = peer.pc.AddICECandidate(c)
	}
}

// maxPendingICE caps candidates buffered before the answer arrives. Real ICE
// gathering yields a handful (host/srflx/relay per interface); the ceiling just
// stops a peer that trickles candidates but never answers from growing the buffer
// unbounded until its connection times out.
const maxPendingICE = 64

// handleWebRTCICE adds a trickled candidate, buffering it if the answer hasn't
// been applied yet (pion rejects candidates before the remote description).
func (a *Agent) handleWebRTCICE(encData string) {
	plaintext, err := a.box.Decrypt(encData)
	if err != nil {
		return
	}
	var m rtcICEMsg
	if json.Unmarshal(plaintext, &m) != nil || m.Peer == "" {
		return
	}
	peer := a.rtcPeerByID(m.Peer)
	if peer == nil {
		return
	}
	cand := webrtc.ICECandidateInit{Candidate: m.Candidate, SDPMid: m.Mid, SDPMLineIndex: m.Line}
	peer.mu.Lock()
	if !peer.haveRemote {
		if len(peer.pendingICE) < maxPendingICE {
			peer.pendingICE = append(peer.pendingICE, cand)
		}
		peer.mu.Unlock()
		return
	}
	peer.mu.Unlock()
	_ = peer.pc.AddICECandidate(cand)
}

func (a *Agent) rtcPeerByID(id string) *rtcPeer {
	a.rtcMu.Lock()
	defer a.rtcMu.Unlock()
	return a.rtcPeers[id]
}

// dropRTCPeer tears down a peer connection and forgets it — but only if it's
// STILL the current peer for id. A reconnecting viewer replaces the map entry
// under the same id and closes the old pc; that old pc's async state-change
// callback lands here, and without the identity guard it would evict the fresh
// peer that just took its place (killing the new P2P transport → WS fallback).
func (a *Agent) dropRTCPeer(id string, p *rtcPeer) {
	a.rtcMu.Lock()
	if a.rtcPeers[id] == p {
		delete(a.rtcPeers, id)
	}
	a.rtcMu.Unlock()
	_ = p.pc.Close()
}

// closeAllRTCPeers tears down every peer connection (e.g. last viewer left).
func (a *Agent) closeAllRTCPeers() {
	a.rtcMu.Lock()
	peers := a.rtcPeers
	a.rtcPeers = nil
	a.rtcMu.Unlock()
	for _, p := range peers {
		_ = p.pc.Close()
	}
}

// rtcSinks classifies open DataChannels into those confirmed to deliver frames
// (send frames over these exclusively — the relay is bypassed) and those still
// being probed (send over these AND WS until one proves itself). A channel that
// has been probed past rtcProbeWindow without ever confirming is closed here —
// it connected but can't carry frames (cellular MTU), so we stop probing it and
// let the viewer re-negotiate later. allH264 reports whether every CONFIRMED
// peer declared WebCodecs H.264 decode (vacuously true with none confirmed) —
// the gate for switching a stream from JPEG to compressed video.
func (a *Agent) rtcSinks() (confirmed, probing []*webrtc.DataChannel, allH264 bool) {
	allH264 = true
	a.rtcMu.Lock()
	var stale []*rtcPeer
	for id, p := range a.rtcPeers {
		switch {
		case !p.open.Load():
			// not ready yet
		case p.confirmed.Load():
			confirmed = append(confirmed, p.dc)
			if !p.h264 {
				allH264 = false
			}
		case p.probeAge() < rtcProbeWindow:
			probing = append(probing, p.dc)
		default:
			stale = append(stale, p)
			delete(a.rtcPeers, id)
		}
	}
	a.rtcMu.Unlock()
	for _, p := range stale {
		_ = p.pc.Close()
	}
	return confirmed, probing, allH264
}

// unconfirmRTC demotes every confirmed peer back to probing (resetting its probe
// clock), used when a confirmed channel goes quiet — frames revert to WS while
// the channel re-proves itself instead of streaming into a silent break.
func (a *Agent) unconfirmRTC() {
	a.rtcMu.Lock()
	defer a.rtcMu.Unlock()
	for _, p := range a.rtcPeers {
		if p.confirmed.Swap(false) {
			p.mu.Lock()
			p.openedAt = time.Now()
			p.mu.Unlock()
		}
	}
}

// viewerCapTTL bounds how long a viewer's announced decode capability is
// trusted without a refresh. It has to outlive the viewer's re-announce
// cadence (~10s) but stay SHORT, because the failure mode is asymmetric: a
// stale record saying "can't decode H.264", left behind by a viewer that has
// since gone, holds every remaining viewer on JPEG until it expires. A viewer
// with a live DataChannel needs no record at all — its peer entry is
// authoritative and disappears the moment the channel does.
const viewerCapTTL = 30 * time.Second

// noteViewerCap records what a viewer said it can decode. Keyed by the peer id
// the viewer minted for this attempt — those churn across retries, which is
// fine: the decision below is "does ANY viewer lack H.264", not a headcount.
func (a *Agent) noteViewerCap(peer string, h264 bool) {
	a.rtcMu.Lock()
	defer a.rtcMu.Unlock()
	if a.viewerCaps == nil {
		a.viewerCaps = map[string]viewerCap{}
	}
	now := time.Now()
	for id, c := range a.viewerCaps { // opportunistic sweep; the map stays tiny
		if now.Sub(c.seen) > viewerCapTTL {
			delete(a.viewerCaps, id)
		}
	}
	a.viewerCaps[peer] = viewerCap{h264: h264, seen: now}
}

// viewersCanH264 reports whether it is safe to put compressed video on the
// wire for EVERY viewer. The relay is a broadcast: one message reaches all of
// them, so a single viewer that can't decode H.264 means nobody gets it.
// Deliberately conservative — it requires positive evidence from at least one
// viewer and no evidence against from any.
func (a *Agent) viewersCanH264() bool {
	a.rtcMu.Lock()
	defer a.rtcMu.Unlock()
	now, any := time.Now(), false
	for _, c := range a.viewerCaps {
		if now.Sub(c.seen) > viewerCapTTL {
			continue
		}
		if !c.h264 {
			return false // an old or incapable viewer is present
		}
		any = true
	}
	for _, p := range a.rtcPeers { // a live peer is authoritative over any record
		if !p.open.Load() {
			continue
		}
		if !p.h264 {
			return false
		}
		any = true // so a P2P viewer needs no periodic re-announcement
	}
	return any
}

// forgetViewerCaps drops every recorded capability. Called when the last viewer
// leaves: records are keyed by a per-attempt id with no disconnect signal
// behind them, so departure is the one moment we can be certain none of them
// describe anybody still watching.
func (a *Agent) forgetViewerCaps() {
	a.rtcMu.Lock()
	a.viewerCaps = nil
	a.rtcMu.Unlock()
}

// viewerCap is one viewer's announced decode capability and when it said so.
type viewerCap struct {
	h264 bool
	seen time.Time
}
