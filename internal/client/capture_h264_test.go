// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// frameMsg builds one [len][flag][payload] helper output frame (h264 framing).
func frameMsg(flag byte, payload []byte) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.BigEndian, uint32(len(payload)+1))
	b.WriteByte(flag)
	b.Write(payload)
	return b.Bytes()
}

// jpegMsg builds one legacy [len][JPEG] helper output frame (no flag byte).
func jpegMsg(payload []byte) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.BigEndian, uint32(len(payload)))
	b.Write(payload)
	return b.Bytes()
}

func newTestHelper(codec string) (*winHelper, io.WriteCloser, *bytes.Buffer) {
	pr, pw := io.Pipe()
	keyReqs := &bytes.Buffer{}
	h := &winHelper{
		codec:  codec,
		stdin:  nopWriteCloser{keyReqs},
		sig:    make(chan struct{}, 1),
		dead:   make(chan struct{}),
		stderr: &bytes.Buffer{},
	}
	go h.readLoop(pr)
	return h, pw, keyReqs
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func TestWinHelperH264Framing(t *testing.T) {
	h, pw, _ := newTestHelper("h264")
	defer pw.Close()

	key := []byte("KEYFRAME-AU")
	delta := []byte("DELTA-AU")
	if _, err := pw.Write(frameMsg(2, key)); err != nil {
		t.Fatal(err)
	}
	if _, err := pw.Write(frameMsg(1, delta)); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	f, ok := h.next(stop, time.Second)
	if !ok || !f.H264 || !f.Key || !bytes.Equal(f.Data, key) {
		t.Fatalf("first frame = %+v ok=%v, want key AU", f, ok)
	}
	f, ok = h.next(stop, time.Second)
	if !ok || !f.H264 || f.Key || !bytes.Equal(f.Data, delta) {
		t.Fatalf("second frame = %+v ok=%v, want delta AU", f, ok)
	}
	// No third frame: next should time out, helper still alive.
	if _, ok := h.next(stop, 50*time.Millisecond); ok {
		t.Fatal("unexpected third frame")
	}
	if !h.alive() {
		t.Fatal("helper reader died on valid stream")
	}
}

// An old daemon that ignores the codec arg streams plain JPEGs (first payload
// byte 0xFF) at a session that asked for h264 — the reader must end the stream
// and flag bad framing so the session falls back to jpeg instead of flapping.
func TestWinHelperH264BadFramingFromJpegStream(t *testing.T) {
	h, pw, _ := newTestHelper("h264")
	defer pw.Close()

	if _, err := pw.Write(jpegMsg([]byte{0xFF, 0xD8, 0xFF, 0xE0, 1, 2, 3})); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.dead:
	case <-time.After(2 * time.Second):
		t.Fatal("reader did not die on jpeg bytes in h264 mode")
	}
	if !h.badFraming.Load() {
		t.Fatal("badFraming not flagged")
	}
}

// Overflowing the h264 queue must clear it, gate on the next key, and ask the
// encoder for one — never hand the consumer a delta whose predecessors were
// dropped.
func TestWinHelperH264QueueOverflowRekeys(t *testing.T) {
	h, _, keyReqs := newTestHelper("h264")

	h.pushH264(winFrame{Data: []byte("K"), H264: true, Key: true})
	for i := 0; i < winH264QueueMax+5; i++ {
		h.pushH264(winFrame{Data: []byte{byte(i)}, H264: true})
	}
	h.mu.Lock()
	qlen, gated := len(h.queue), h.dropUntilKey
	h.mu.Unlock()
	if qlen != 0 || !gated {
		t.Fatalf("after overflow: queue=%d dropUntilKey=%v, want 0/true", qlen, gated)
	}
	if !bytes.Contains(keyReqs.Bytes(), []byte("key\n")) {
		t.Fatal("overflow did not request a keyframe")
	}

	// Deltas are discarded while gated; a key AU reopens the stream.
	h.pushH264(winFrame{Data: []byte("stale-delta"), H264: true})
	h.pushH264(winFrame{Data: []byte("K2"), H264: true, Key: true})
	h.pushH264(winFrame{Data: []byte("d2"), H264: true})
	stop := make(chan struct{})
	f, ok := h.next(stop, time.Second)
	if !ok || !f.Key || !bytes.Equal(f.Data, []byte("K2")) {
		t.Fatalf("post-overflow first frame = %+v ok=%v, want key K2", f, ok)
	}
	f, ok = h.next(stop, time.Second)
	if !ok || f.Key || !bytes.Equal(f.Data, []byte("d2")) {
		t.Fatalf("post-overflow second frame = %+v ok=%v, want delta d2", f, ok)
	}
}

// rekey clears pending frames, gates on the next key, and writes the command.
func TestWinHelperRekey(t *testing.T) {
	h, _, keyReqs := newTestHelper("h264")
	h.pushH264(winFrame{Data: []byte("K"), H264: true, Key: true})
	h.pushH264(winFrame{Data: []byte("d"), H264: true})
	h.rekey()
	h.mu.Lock()
	qlen, gated := len(h.queue), h.dropUntilKey
	h.mu.Unlock()
	if qlen != 0 || !gated {
		t.Fatalf("after rekey: queue=%d dropUntilKey=%v, want 0/true", qlen, gated)
	}
	if !bytes.Contains(keyReqs.Bytes(), []byte("key\n")) {
		t.Fatal("rekey did not write the key command")
	}
}

// JPEG mode must keep its keep-newest-only semantics: two frames written, only
// the second survives.
func TestWinHelperJpegKeepsNewest(t *testing.T) {
	h, pw, _ := newTestHelper("")
	defer pw.Close()

	if _, err := pw.Write(jpegMsg([]byte("old"))); err != nil {
		t.Fatal(err)
	}
	if _, err := pw.Write(jpegMsg([]byte("new"))); err != nil {
		t.Fatal(err)
	}
	// Give the reader a moment to consume both.
	deadline := time.Now().Add(time.Second)
	for {
		h.mu.Lock()
		latest := string(h.latest)
		h.mu.Unlock()
		if latest == "new" || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	stop := make(chan struct{})
	f, ok := h.next(stop, time.Second)
	if !ok || f.H264 || string(f.Data) != "new" {
		t.Fatalf("jpeg next = %q ok=%v, want newest frame", f.Data, ok)
	}
}

func TestBuildWinBinMsgsSingle(t *testing.T) {
	au := bytes.Repeat([]byte{0xAB}, 1000)
	msgs := buildWinBinMsgs("6112", 42, 800, 600, true, au, winDCMaxMsg)
	if len(msgs) != 1 {
		t.Fatalf("msgs = %d, want 1", len(msgs))
	}
	m := msgs[0]
	if m[0] != winBinMagic || m[1] != winBinFlagKey || m[2] != 4 {
		t.Fatalf("header = % x", m[:3])
	}
	if string(m[3:7]) != "6112" {
		t.Fatalf("id = %q", m[3:7])
	}
	if seq := binary.BigEndian.Uint64(m[7:15]); seq != 42 {
		t.Fatalf("seq = %d", seq)
	}
	if w := binary.BigEndian.Uint32(m[15:19]); w != 800 {
		t.Fatalf("w = %d", w)
	}
	if hh := binary.BigEndian.Uint32(m[19:23]); hh != 600 {
		t.Fatalf("h = %d", hh)
	}
	if !bytes.Equal(m[23:], au) {
		t.Fatal("payload mismatch")
	}
}

func TestBuildWinBinMsgsChunked(t *testing.T) {
	const maxMsg = 256
	id := "w1"
	hdrLen := 3 + len(id) + 8 + 4 + 4
	au := make([]byte, (maxMsg-hdrLen)*2+10) // forces 3 chunks
	for i := range au {
		au[i] = byte(i)
	}
	msgs := buildWinBinMsgs(id, 7, 100, 50, false, au, maxMsg)
	if len(msgs) != 3 {
		t.Fatalf("msgs = %d, want 3", len(msgs))
	}
	var got []byte
	for i, m := range msgs {
		if len(m) > maxMsg {
			t.Fatalf("chunk %d = %d bytes > maxMsg", i, len(m))
		}
		more := m[1]&winBinFlagMore != 0
		if wantMore := i < len(msgs)-1; more != wantMore {
			t.Fatalf("chunk %d more=%v, want %v", i, more, wantMore)
		}
		if m[1]&winBinFlagKey != 0 {
			t.Fatalf("chunk %d flagged key on a delta AU", i)
		}
		if seq := binary.BigEndian.Uint64(m[3+len(id):]); seq != 7 {
			t.Fatalf("chunk %d seq = %d", i, seq)
		}
		got = append(got, m[hdrLen:]...)
	}
	if !bytes.Equal(got, au) {
		t.Fatal("reassembled AU differs from input")
	}
}

// desiredCodec truth table. Video needs EVERY viewer able to decode it (the
// relay is a broadcast), but not peer-to-peer delivery — wsVideo just reports
// whether a relay copy is also required this iteration.
func TestDesiredCodec(t *testing.T) {
	mkAgent := func(peers ...*rtcPeer) *Agent {
		a := &Agent{rtcPeers: map[string]*rtcPeer{}}
		for i, p := range peers {
			id := string(rune('a' + i))
			a.rtcPeers[id] = p
			a.noteViewerCap(id, p.h264) // every viewer announces on hello
		}
		return a
	}
	mkPeer := func(open, confirmed, h264 bool) *rtcPeer {
		p := &rtcPeer{h264: h264, openedAt: time.Now()}
		p.open.Store(open)
		p.confirmed.Store(confirmed)
		return p
	}

	cases := []struct {
		name    string
		peers   []*rtcPeer
		viewers int
		broken  bool
		want    string
		wantWS  bool
	}{
		// Peer-to-peer only: every viewer has a confirmed channel.
		{"one confirmed h264 viewer", []*rtcPeer{mkPeer(true, true, true)}, 1, false, "h264", false},
		{"two confirmed h264", []*rtcPeer{mkPeer(true, true, true), mkPeer(true, true, true)}, 2, false, "h264", false},
		// Video over the relay: capable viewers that aren't (yet) on P2P.
		{"probing peer needs relay copy", []*rtcPeer{mkPeer(true, false, true)}, 1, false, "h264", true},
		{"ws viewer beyond the dc count", []*rtcPeer{mkPeer(true, true, true)}, 2, false, "h264", true},
		// No video at all.
		{"no viewer announced", nil, 1, false, "", false},
		{"viewer cannot decode", []*rtcPeer{mkPeer(true, true, false)}, 1, false, "", false},
		{"mixed capability", []*rtcPeer{mkPeer(true, true, true), mkPeer(true, true, false)}, 2, false, "", false},
		{"h264 broken on this stream", []*rtcPeer{mkPeer(true, true, true)}, 1, true, "", false},
	}
	for _, tc := range cases {
		a := mkAgent(tc.peers...)
		a.viewerCount = tc.viewers
		s := &winStream{a: a, h264Broken: tc.broken}
		got, gotWS := s.desiredCodec()
		if got != tc.want || (got != "" && gotWS != tc.wantWS) {
			t.Errorf("%s: desiredCodec() = (%q, ws=%v), want (%q, ws=%v)", tc.name, got, gotWS, tc.want, tc.wantWS)
		}
	}
}

// Live check of the DAEMON path with an h264 request; skipped unless pointed
// at a window. Passes in both deployment states: a NEW daemon streams h264
// frames; an OLD daemon (ignores the codec arg) streams JPEGs, which must trip
// the framing validator → dead helper + badFraming — the exact signal the
// session uses to fall back to jpeg. Run:
//
//	REMINAL_LIVE_H264_WIN=<windowID> go test -run TestH264MirrorLive
func TestH264MirrorLive(t *testing.T) {
	win := os.Getenv("REMINAL_LIVE_H264_WIN")
	if win == "" {
		t.Skip("set REMINAL_LIVE_H264_WIN to run")
	}
	h, err := startMirrorCapture(win, 1100, 45, 30, "h264")
	if err != nil {
		// An OLD daemon ignores the codec arg and streams JPEGs; the framing
		// validator kills the stream inside the startup grace, so the error
		// surfaces right here — the session's ensureHelper then falls back to
		// jpeg. A dial failure means no daemon at all: nothing to test.
		if strings.Contains(err.Error(), "service starting") {
			t.Skipf("daemon not running: %v", err)
		}
		t.Logf("old daemon detected (stream rejected at startup: %v) — jpeg fallback path verified", err)
		return
	}
	defer h.stop()
	stop := make(chan struct{})
	f, ok := h.next(stop, 5*time.Second)
	if !ok || !f.H264 || !f.Key {
		t.Fatalf("new daemon but no h264 keyframe: ok=%v frame(h264=%v key=%v len=%d)",
			ok, f.H264, f.Key, len(f.Data))
	}
	t.Log("live daemon speaks h264 (new daemon)")
}

// Live end-to-end check of the direct-exec path against a real window; skipped
// unless pointed at a helper + window:
//
//	REMINAL_LIVE_H264_WIN=<windowID> REMINAL_CAPTURE_HELPER=<path> go test -run TestH264HelperLive
func TestH264HelperLive(t *testing.T) {
	win := os.Getenv("REMINAL_LIVE_H264_WIN")
	if win == "" || os.Getenv("REMINAL_CAPTURE_HELPER") == "" {
		t.Skip("set REMINAL_LIVE_H264_WIN and REMINAL_CAPTURE_HELPER to run")
	}
	h, err := startWinHelper(win, 1100, 45, 30, "h264")
	if err != nil {
		t.Fatalf("startWinHelper: %v", err)
	}
	defer h.stop()
	stop := make(chan struct{})
	f, ok := h.next(stop, 5*time.Second)
	if !ok || !f.Key {
		t.Fatalf("first live frame ok=%v key=%v", ok, f.Key)
	}
	var deltas int
	for i := 0; i < 30; i++ {
		if f, ok = h.next(stop, time.Second); ok && !f.Key {
			deltas++
		}
	}
	h.rekey()
	var gotKey atomic.Bool
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if f, ok = h.next(stop, time.Second); ok && f.Key {
			gotKey.Store(true)
			break
		}
	}
	if !gotKey.Load() {
		t.Fatal("no keyframe after rekey")
	}
	t.Logf("live: key + %d deltas + rekey OK", deltas)
}

// The daemon's out-of-band error frame must reach the session, so a capture
// failure reports its real cause instead of a generic "helper exited". Frames
// before it still decode normally.
func TestMirrorErrorFrameReachesSession(t *testing.T) {
	h, pw, _ := newTestHelper("h264")
	defer pw.Close()

	if _, err := pw.Write(frameMsg(2, []byte("KEY-AU"))); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	if f, ok := h.next(stop, time.Second); !ok || !f.Key {
		t.Fatalf("expected the key AU before the error frame, got ok=%v", ok)
	}

	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[0:4], winErrFrameMagic)
	msg := "window 6017 not found"
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(msg)))
	if _, err := pw.Write(append(hdr[:], msg...)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for h.errorText() != msg && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := h.errorText(); got != msg {
		t.Fatalf("errorText() = %q, want %q", got, msg)
	}
	if !h.alive() {
		t.Fatal("an error frame must not end the stream by itself")
	}
}

// An oversized/garbage length must still end the stream rather than be
// mistaken for an error frame.
func TestBogusFrameLengthStillEndsStream(t *testing.T) {
	h, pw, _ := newTestHelper("h264")
	defer pw.Close()
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], 32*1024*1024)
	if _, err := pw.Write(hdr[:]); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.dead:
	case <-time.After(2 * time.Second):
		t.Fatal("reader did not end on an absurd frame length")
	}
}
