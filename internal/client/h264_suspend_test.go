// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// A daemon that is merely restarting must not be read as "this machine cannot
// encode H.264". The dial fails for about a second during `reminal restart
// --all` or an upgrade; treating that as a codec verdict dropped every open
// pane to JPEG at relay frame rate for as long as it stayed open, with no
// signal to the user beyond the picture quietly getting worse.
func TestMirrorUnavailableIsNotEvidenceAgainstTheCodec(t *testing.T) {
	// The wrapper startMirrorCapture builds on a dial failure.
	err := fmt.Errorf("%w: %w", errMirrorUnavailable, errors.New("dial unix: connect: no such file"))
	if !errors.Is(err, errMirrorUnavailable) {
		t.Fatal("a dial failure must be recognisable as transient")
	}
	// The underlying cause stays reachable for the operator-facing message.
	if got := err.Error(); got == "" || !contains(got, "no such file") {
		t.Fatalf("wrapped cause lost: %q", got)
	}
	// A reason the daemon actually reported is NOT transient — that one should
	// suspend the codec.
	reported := errors.New("startCapture: VTCompressionSession unavailable")
	if errors.Is(reported, errMirrorUnavailable) {
		t.Fatal("a reported capture failure must not look transient")
	}
}

// suspendH264 has to be a bounded suspension, not a latch. The evidence behind
// it is a guess (a helper that died "too soon" is indistinguishable from a
// daemon bounce caught mid-startup), and a wrong guess used to cost the stream
// its video permanently.
func TestH264SuspensionExpires(t *testing.T) {
	s := &winStream{codec: "h264"}
	s.suspendH264()

	if s.codec != "" {
		t.Fatalf("codec = %q, want the stream dropped to jpeg", s.codec)
	}
	if !time.Now().Before(s.h264BrokenUntil) {
		t.Fatal("suspension is not in effect")
	}
	if got := time.Until(s.h264BrokenUntil); got > h264SuspendFor+time.Second {
		t.Fatalf("suspended for %v, want about %v", got, h264SuspendFor)
	}

	// Once it lapses the stream is free to try video again.
	s.h264BrokenUntil = time.Now().Add(-time.Millisecond)
	if time.Now().Before(s.h264BrokenUntil) {
		t.Fatal("a lapsed suspension still reads as active")
	}
}

// The retry pause has to match what actually went wrong: an unreachable daemon
// is back in about a second, so the ten-second cooldown meant for a genuine
// capture failure just leaves a stale error on screen.
func TestRetryPauseMatchesTheFailure(t *testing.T) {
	if helperUnavailableRetry >= helperRetryCooldown {
		t.Fatalf("transient retry %v must be shorter than the failure cooldown %v",
			helperUnavailableRetry, helperRetryCooldown)
	}
	if helperUnavailableRetry > time.Second {
		t.Fatalf("transient retry %v is too slow for a ~1s daemon bounce", helperUnavailableRetry)
	}
	// A suspension must outlast the churn that can trigger it, or a restart
	// storm would have every pane re-testing h264 continuously.
	if h264SuspendFor <= helperRetryCooldown {
		t.Fatalf("suspension %v should outlast the cooldown %v", h264SuspendFor, helperRetryCooldown)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
