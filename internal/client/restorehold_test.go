// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"testing"
	"time"
)

// A restored session holds onto the agent it had only until that agent is
// running again. Holding it past that point meant quitting the agent inside
// the settle window left the record still naming it, and the next restart
// brought back an agent the person had deliberately closed.
func TestHoldPreviousAgent(t *testing.T) {
	cases := []struct {
		name      string
		restoring bool
		agentSeen bool
		atPrompt  bool
		since     time.Duration
		want      bool
	}{
		// Not at a prompt: something else is in front for a moment (a pager the
		// agent opened). That is not the agent ending, whatever else is true.
		{"a pager is in front, normal session", false, false, false, time.Hour, true},
		{"a pager is in front, just restored", true, false, false, time.Second, true},

		// At the shell's own prompt, mid-restore: the agent has not been
		// started again yet, and sessions restored after this one still need
		// to know what it was running.
		{"restoring, agent not back yet", true, false, true, 5 * time.Second, true},

		// The case this exists for: the agent came back, the person quit it.
		{"restoring, agent seen, then quit", true, true, true, 5 * time.Second, false},

		// The settle window still ends on its own if the agent never appears.
		{"restoring, agent never appeared, window passed", true, false, true, restoreSettle + time.Second, false},

		// An ordinary session at its prompt records that nothing is running.
		{"not restoring, at prompt", false, false, true, time.Hour, false},
		{"not restoring, agent seen, at prompt", false, true, true, time.Hour, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := holdPreviousAgent(c.restoring, c.agentSeen, c.atPrompt, c.since); got != c.want {
				t.Fatalf("holdPreviousAgent(restoring=%v, seen=%v, atPrompt=%v, since=%v) = %v, want %v",
					c.restoring, c.agentSeen, c.atPrompt, c.since, got, c.want)
			}
		})
	}
}
