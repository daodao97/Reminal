// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// replaydFakes swaps every seam for a fake and restores them after the test.
type replaydFakes struct {
	mu      sync.Mutex
	granted bool
	pids    []int
	killed  []int
	killErr error
	logs    []string
	now     time.Time
}

func withReplaydFakes(t *testing.T, granted bool, pids []int) *replaydFakes {
	t.Helper()
	f := &replaydFakes{granted: granted, pids: pids, now: time.Date(2026, 10, 3, 16, 30, 0, 0, time.UTC)}
	oldGOOS, oldGranted, oldPIDs, oldKill, oldLog, oldNow := replaydHealGOOS, replaydCaptureGranted, replaydPIDs, replaydKill, replaydLog, replaydNow
	replaydHeal.Lock()
	oldLast := replaydHeal.last
	replaydHeal.last = time.Time{}
	replaydHeal.Unlock()
	replaydHealGOOS = "darwin"
	replaydCaptureGranted = func() bool { return f.granted }
	replaydPIDs = func() []int { return f.pids }
	replaydKill = func(pid int) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.killErr != nil {
			return f.killErr
		}
		f.killed = append(f.killed, pid)
		return nil
	}
	replaydLog = func(format string, a ...any) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.logs = append(f.logs, format)
	}
	replaydNow = func() time.Time { return f.now }
	t.Cleanup(func() {
		replaydHealGOOS, replaydCaptureGranted, replaydPIDs, replaydKill, replaydLog, replaydNow = oldGOOS, oldGranted, oldPIDs, oldKill, oldLog, oldNow
		replaydHeal.Lock()
		replaydHeal.last = oldLast
		replaydHeal.Unlock()
	})
	return f
}

func TestHealStuckReplaydRestartsWhenGranted(t *testing.T) {
	f := withReplaydFakes(t, true, []int{28020})
	if !healStuckReplayd() {
		t.Fatal("expected a restart when capture is granted and replayd is running")
	}
	if len(f.killed) != 1 || f.killed[0] != 28020 {
		t.Fatalf("killed %v, want [28020]", f.killed)
	}
	if len(f.logs) != 1 {
		t.Fatalf("expected one log line, got %v", f.logs)
	}
}

func TestHealStuckReplaydLeavesAGenuineNoAlone(t *testing.T) {
	f := withReplaydFakes(t, false, []int{28020})
	if healStuckReplayd() {
		t.Fatal("must not restart replayd when Screen Recording is really not granted")
	}
	if len(f.killed) != 0 {
		t.Fatalf("killed %v, want nothing", f.killed)
	}
}

func TestHealStuckReplaydCooldown(t *testing.T) {
	f := withReplaydFakes(t, true, []int{28020})
	if !healStuckReplayd() {
		t.Fatal("first heal should restart")
	}
	f.now = f.now.Add(replaydHealCooldown - time.Second)
	if healStuckReplayd() {
		t.Fatal("a second heal inside the cooldown must not restart again")
	}
	f.now = f.now.Add(2 * time.Second)
	if !healStuckReplayd() {
		t.Fatal("after the cooldown a heal may run again")
	}
	if len(f.killed) != 2 {
		t.Fatalf("killed %v, want two restarts in total", f.killed)
	}
}

func TestHealStuckReplaydNothingToRestart(t *testing.T) {
	f := withReplaydFakes(t, true, nil)
	if healStuckReplayd() {
		t.Fatal("no replayd running means nothing to restart")
	}
	f.pids = []int{1234}
	f.killErr = errors.New("operation not permitted")
	f.now = f.now.Add(replaydHealCooldown)
	if healStuckReplayd() {
		t.Fatal("a failed kill must not be reported as a restart")
	}
}

func TestNoteCaptureFailureOnlyReactsToTheSymptom(t *testing.T) {
	f := withReplaydFakes(t, true, []int{28020})
	noteCaptureFailure("capture 39007 ended: window 39007 not found")
	noteCaptureFailure("capture display:1 ended: stream stopped: Failed to find any displays or windows to capture")
	time.Sleep(50 * time.Millisecond)
	f.mu.Lock()
	n := len(f.killed)
	f.mu.Unlock()
	if n != 0 {
		t.Fatalf("unrelated capture failures must not restart replayd (killed %v)", f.killed)
	}
	noteCaptureFailure("capture display:1 ended: shareable content (screen recording permission?): The user declined TCCs for application, window, display capture")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		n = len(f.killed)
		f.mu.Unlock()
		if n == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the declined-TCCs failure should restart replayd once (killed %v)", f.killed)
}

func TestNoteCaptureFailureIgnoredOffMacOS(t *testing.T) {
	f := withReplaydFakes(t, true, []int{28020})
	replaydHealGOOS = "linux"
	noteCaptureFailure("The user declined TCCs for application, window, display capture")
	time.Sleep(50 * time.Millisecond)
	if len(f.killed) != 0 {
		t.Fatal("only macOS has replayd")
	}
}

func TestLineLoggerHandsFinishedLinesToOnLine(t *testing.T) {
	var got []string
	l := &lineLogger{prefix: "test: ", onLine: func(s string) { got = append(got, s) }}
	_, _ = l.Write([]byte("first line\nsecond "))
	_, _ = l.Write([]byte("half\n"))
	if len(got) != 2 || got[0] != "first line" || got[1] != "second half" {
		t.Fatalf("onLine got %q", got)
	}
}

func TestParsePIDs(t *testing.T) {
	got := parsePIDs("28020\n  99 \nnot-a-pid\n1\n")
	if len(got) != 2 || got[0] != 28020 || got[1] != 99 {
		t.Fatalf("parsePIDs = %v", got)
	}
}
