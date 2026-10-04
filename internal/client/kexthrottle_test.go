// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"testing"
	"time"
)

// TestKexThrottle verifies the agent's kex token bucket: it allows an initial
// burst (so several viewers / a few PIN fat-fingers connect fine) then rate-
// limits, and refills over time. Each answered kex_init is one online PIN
// guess, so this is what bounds a malicious relay's brute-force.
func TestKexThrottle(t *testing.T) {
	a := &Agent{}
	base := time.Unix(1_700_000_000, 0)

	// Burst: the first kexBurst attempts at t0 all pass.
	for i := 0; i < kexBurst; i++ {
		if !a.allowKex(base) {
			t.Fatalf("attempt %d within burst should be allowed", i)
		}
	}
	// The next one (still t0) is throttled — bucket drained.
	if a.allowKex(base) {
		t.Fatalf("attempt past the burst at the same instant should be throttled")
	}

	// After one refill interval, exactly one more token is available.
	if !a.allowKex(base.Add(kexRefill)) {
		t.Fatalf("one token should have refilled after kexRefill")
	}
	if a.allowKex(base.Add(kexRefill)) {
		t.Fatalf("only one token should refill per interval")
	}

	// The bucket never exceeds kexBurst even after a long idle stretch.
	far := base.Add(1000 * kexRefill)
	for i := 0; i < kexBurst; i++ {
		if !a.allowKex(far) {
			t.Fatalf("post-idle burst attempt %d should be allowed", i)
		}
	}
	if a.allowKex(far) {
		t.Fatalf("bucket should be capped at kexBurst even after long idle")
	}
}

// The short-term pace alone let a patient guesser try the PIN thousands of
// times a day. The long-term allowance is what bounds that: once it is spent,
// attempts are answered only as fast as it refills, however long they keep
// coming.
func TestKexLongTermAllowance(t *testing.T) {
	a := &Agent{}
	now := time.Unix(1_700_000_000, 0)

	// Someone trying as fast as the short-term pace allows, for a whole day.
	answered := 0
	for end := now.Add(24 * time.Hour); now.Before(end); now = now.Add(kexRefill) {
		if a.allowKex(now) {
			answered++
		}
	}
	ceiling := kexLongBurst + int((24*time.Hour)/kexLongRefill) + 1
	if answered > ceiling {
		t.Fatalf("answered %d PIN handshakes in a day, want at most %d", answered, ceiling)
	}
	if answered < kexLongBurst {
		t.Fatalf("answered only %d — the allowance should cover at least its burst", answered)
	}

	// And ordinary use is untouched: after a quiet stretch a person can still
	// connect several times in a row.
	now = now.Add(48 * time.Hour)
	for i := 0; i < kexBurst; i++ {
		if !a.allowKex(now) {
			t.Fatalf("connect %d after a quiet stretch was refused", i)
		}
	}
}

// One source on its own gets exactly what the machine-wide allowance gives:
// tagging it never makes it wait longer.
func TestKexSourceShareAloneMatchesMachineWide(t *testing.T) {
	run := func(src string) int {
		a := &Agent{}
		base := time.Now()
		n := 0
		for i := 0; i < 180; i++ { // a handshake a minute for three hours
			if ok, _ := a.allowKexFrom(base.Add(time.Duration(i)*time.Minute), src); ok {
				n++
			}
		}
		return n
	}
	if tagged, untagged := run("A"), run(""); tagged != untagged {
		t.Fatalf("one tagged source got %d handshakes, untagged got %d", tagged, untagged)
	}
}

// Once the machine-wide allowance is low, a source past its share waits and
// another source is still served.
func TestKexSourceShareUnderPressure(t *testing.T) {
	a := &Agent{}
	now := time.Now()
	a.allowKexFrom(now, "B") // B is about too
	for i := 0; i < 7; i++ {
		if ok, _ := a.allowKexFrom(now, "A"); !ok {
			t.Fatalf("A refused at %d within the burst", i)
		}
	}
	// Spread the rest out so the short-term bucket is not what refuses.
	at := now
	for i := 0; i < 20; i++ {
		at = at.Add(kexRefill)
		a.allowKexFrom(at, "A")
	}
	at = at.Add(kexRefill)
	ok, retry := a.allowKexFrom(at, "A")
	if ok || retry <= 0 {
		t.Fatalf("A past its share under pressure: ok=%v retry=%v", ok, retry)
	}
	if ok, _ := a.allowKexFrom(at.Add(kexRefill), "B"); !ok {
		t.Fatal("another source was refused while the machine still had allowance")
	}
}

// Two sources over three hours, one asking every 10 s and one every 3 min:
// the second keeps getting answered, with no long run of refusals.
func TestKexSteadySourceKeepsBeingAnswered(t *testing.T) {
	a := &Agent{}
	base := time.Now()
	got, longestGap := 0, time.Duration(0)
	last := base
	for sec := 0; sec < 3*3600; sec += 10 {
		now := base.Add(time.Duration(sec) * time.Second)
		a.allowKexFrom(now, "frequent")
		if sec%180 != 0 {
			continue
		}
		if ok, _ := a.allowKexFrom(now, "steady"); ok {
			got++
			last = now
		} else if g := now.Sub(last); g > longestGap {
			longestGap = g
		}
	}
	if got < 30 || longestGap > 15*time.Minute {
		t.Fatalf("steady source: %d of 60 answered, longest gap %v", got, longestGap)
	}
}
