// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"reflect"
	"testing"

	"reminal/internal/session"
)

func TestResumeArgv(t *testing.T) {
	cases := []struct {
		name string
		r    session.Restore
		want []string
	}{
		{"claude by id keeps its flags, drops its prompt",
			session.Restore{Fg: "claude", FgArgs: []string{"node", "/opt/claude/cli.js", "--model", "opus", "--dangerously-skip-permissions", "fix the tests"}, Conv: "a1b2c3d4-e5f6"},
			[]string{"claude", "--model", "opus", "--dangerously-skip-permissions", "--resume", "a1b2c3d4-e5f6"}},
		{"claude without an id continues the latest",
			session.Restore{Fg: "claude", FgArgs: []string{"claude"}},
			[]string{"claude", "--continue"}},
		{"an old resume is replaced, not doubled",
			session.Restore{Fg: "claude", FgArgs: []string{"claude", "--resume", "old-conversation", "-c"}, Conv: "new-conversation"},
			[]string{"claude", "--resume", "new-conversation"}},
		{"print mode was never interactive",
			session.Restore{Fg: "claude", FgArgs: []string{"claude", "-p", "hi"}}, nil},
		{"codex by id",
			session.Restore{Fg: "codex", FgArgs: []string{"codex", "--full-auto"}, Conv: "0199aaaa-bbbb"},
			[]string{"codex", "resume", "0199aaaa-bbbb"}},
		{"codex exec is one-shot",
			session.Restore{Fg: "codex", FgArgs: []string{"codex", "exec", "do it"}}, nil},
		{"cursor-agent latest",
			session.Restore{Fg: "cursor-agent", FgArgs: []string{"cursor-agent", "-f"}},
			[]string{"cursor-agent", "resume"}},
		{"not an agent", session.Restore{Fg: "vim", FgArgs: []string{"vim", "x"}}, nil},
	}
	for _, c := range cases {
		if got := resumeArgv(c.r); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestShellJoinQuotes(t *testing.T) {
	got := shellJoin([]string{"claude", "--append-system-prompt", "be brief; rm -rf /", "--model", "o'pus"})
	want := `claude --append-system-prompt 'be brief; rm -rf /' --model 'o'\''pus'`
	if got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}
