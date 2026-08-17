// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

// An ack on the ctl channel must still be able to CONFIRM the frame channel,
// or a v2 viewer could never get off the relay: confirmation means "full-size
// frames traverse the frame channel", and a small message arriving on a
// channel of its own is no evidence of that by itself. The viewer supplies the
// evidence instead, by stamping dc on acks for frames that came peer-to-peer.
func TestCtlAckConfirmsOnlyWhenItSaysSo(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      string
		viaFrames bool
		want      bool
	}{
		{"frame-channel ack confirms", `{"id":"w1","seq":4}`, true, true},
		{"bare ctl ack does not confirm", `{"id":"w1","seq":4}`, false, false},
		{"ctl ack stamped dc confirms", `{"id":"w1","seq":4,"dc":true}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &Agent{winAck: map[string]chan uint64{"w1": make(chan uint64, 4)}}
			p := &rtcPeer{}
			a.onRTCViewerMsg(p, []byte(tc.body), tc.viaFrames)
			if got := p.confirmed.Load(); got != tc.want {
				t.Fatalf("confirmed = %v, want %v", got, tc.want)
			}
			select {
			case seq := <-a.winAck["w1"]:
				if seq != 4 {
					t.Fatalf("ack seq = %d, want 4", seq)
				}
			default:
				t.Fatal("ack was not delivered to the pacing loop")
			}
		})
	}
}

// A keyframe request arriving over a DataChannel used to be unmarshalled into
// a struct with no Key field and silently dropped, so a viewer that lost sync
// peer-to-peer waited out the encoder's periodic IDR instead of getting the
// one it asked for. Dropped frames make that request routine, so it has to
// land.
func TestCtlKeyRequestReachesTheStream(t *testing.T) {
	var flag atomic.Bool
	a := &Agent{
		winAck:    map[string]chan uint64{"w1": make(chan uint64, 4)},
		winKeyReq: map[string]*atomic.Bool{"w1": &flag},
	}
	a.onRTCViewerMsg(&rtcPeer{}, []byte(`{"id":"w1","seq":9,"key":true,"dc":true}`), false)
	if !flag.Load() {
		t.Fatal("keyframe request did not reach the stream")
	}
}

// Malformed or unaddressed messages must not confirm a channel or panic — this
// is viewer-supplied data arriving on pion's read goroutine.
func TestViewerMsgIgnoresJunk(t *testing.T) {
	a := &Agent{winAck: map[string]chan uint64{}}
	for _, body := range []string{``, `not json`, `{}`, `{"seq":3}`, `[1,2,3]`, `{"id":""}`} {
		p := &rtcPeer{}
		a.onRTCViewerMsg(p, []byte(body), true)
		if p.confirmed.Load() {
			t.Fatalf("%q confirmed the channel", body)
		}
	}
}

// The v2 channel settings have to be ones pion will actually negotiate, and
// both channels have to reach the answering peer under distinguishable labels
// — the viewer routes by label, so a mislabelled pair would bind the frame
// handler to the control channel.
func TestV2ChannelsNegotiate(t *testing.T) {
	offerer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Skipf("no peer connection in this environment: %v", err)
	}
	defer offerer.Close()
	answerer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Skipf("no peer connection in this environment: %v", err)
	}
	defer answerer.Close()

	ordered, lifetime := false, uint16(rtcFrameLifetimeMS)
	frames, err := offerer.CreateDataChannel("frames", &webrtc.DataChannelInit{Ordered: &ordered, MaxPacketLifeTime: &lifetime})
	if err != nil {
		t.Fatalf("create frames channel: %v", err)
	}
	retransmits := uint16(0)
	if _, err := offerer.CreateDataChannel("ctl", &webrtc.DataChannelInit{Ordered: &ordered, MaxRetransmits: &retransmits}); err != nil {
		t.Fatalf("create ctl channel: %v", err)
	}
	if frames.Ordered() {
		t.Fatal("frame channel negotiated as ordered — head-of-line blocking is back")
	}

	seen := make(chan string, 4)
	answerer.OnDataChannel(func(d *webrtc.DataChannel) { seen <- d.Label() })

	// Wire the two ends together directly; no ICE server, no relay.
	offerer.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			_ = answerer.AddICECandidate(c.ToJSON())
		}
	})
	answerer.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			_ = offerer.AddICECandidate(c.ToJSON())
		}
	})
	offer, err := offerer.CreateOffer(nil)
	if err != nil {
		t.Fatalf("offer: %v", err)
	}
	if err := offerer.SetLocalDescription(offer); err != nil {
		t.Fatalf("set local: %v", err)
	}
	if err := answerer.SetRemoteDescription(offer); err != nil {
		t.Fatalf("set remote: %v", err)
	}
	answer, err := answerer.CreateAnswer(nil)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if err := answerer.SetLocalDescription(answer); err != nil {
		t.Fatalf("answer set local: %v", err)
	}
	if err := offerer.SetRemoteDescription(answer); err != nil {
		t.Fatalf("answer set remote: %v", err)
	}

	got := map[string]bool{}
	deadline := time.After(15 * time.Second)
	for len(got) < 2 {
		select {
		case label := <-seen:
			got[label] = true
		case <-deadline:
			t.Fatalf("only saw channels %v before timing out", got)
		}
	}
	if !got["frames"] || !got["ctl"] {
		t.Fatalf("channels = %v, want both frames and ctl", got)
	}
}
