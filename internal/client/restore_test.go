// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"reflect"
	"strings"
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
		{"qwen by id",
			session.Restore{Fg: "qwen", FgArgs: []string{"node", "/usr/local/bin/qwen"}, Conv: "2e03a898-8ab6"},
			[]string{"qwen", "--resume", "2e03a898-8ab6"}},
		{"gemini resumes only by index or latest",
			session.Restore{Fg: "gemini", FgArgs: []string{"node", "/usr/local/bin/gemini", "--resume", "latest"}, Conv: "6b229456-6e4c"},
			[]string{"gemini", "--resume", "latest"}},
		{"pi keeps a provider given as a flag",
			session.Restore{Fg: "pi", FgArgs: []string{"pi", "--provider", "fake", "-e", "/x/p.ts"}},
			[]string{"pi", "--provider", "fake", "-e", "/x/p.ts", "--continue"}},
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

// Several sessions of one agent in one folder, all ended by one reboot: an
// exact id resumes exactly; "latest" would hand every one of them the same
// conversation, so the agent's own list is opened instead — or, with none,
// nothing is started and the note says how.
func TestResumePlanSharedFolder(t *testing.T) {
	a := session.Restore{ID: "A", Fg: "claude", FgArgs: []string{"claude"}, Cwd: "/w", Conv: "11111111-aaaa"}
	b := session.Restore{ID: "B", Fg: "claude", FgArgs: []string{"claude"}, Cwd: "/w/", Conv: "22222222-bbbb"}
	c := session.Restore{ID: "C", Fg: "claude", FgArgs: []string{"claude", "--model", "opus"}, Cwd: "/w"} // never reported an id
	all := []session.Restore{a, b, c}

	for _, x := range []session.Restore{a, b} {
		argv, note := resumePlan(x, all)
		if want := []string{"claude", "--resume", x.Conv}; !reflect.DeepEqual(argv, want) || note != "" {
			t.Errorf("%s: got %q %q, want its own conversation %q", x.ID, argv, note, want)
		}
	}
	if argv, note := resumePlan(c, all); !reflect.DeepEqual(argv, []string{"claude", "--model", "opus", "--resume"}) || note == "" {
		t.Errorf("C (no id, folder shared): got %q %q, want the picker and a note", argv, note)
	}
	// Alone in its folder, "latest" is exact.
	if argv, note := resumePlan(c, []session.Restore{c, {ID: "D", Fg: "claude", Cwd: "/elsewhere"}, {ID: "E", Fg: "codex", Cwd: "/w"}}); !reflect.DeepEqual(argv, []string{"claude", "--model", "opus", "--continue"}) || note != "" {
		t.Errorf("C alone: got %q %q, want --continue", argv, note)
	}

	x := session.Restore{ID: "X", Fg: "codex", FgArgs: []string{"codex"}, Cwd: "/w"}
	y := session.Restore{ID: "Y", Fg: "codex", FgArgs: []string{"codex"}, Cwd: "/w"}
	if argv, _ := resumePlan(x, []session.Restore{x, y}); !reflect.DeepEqual(argv, []string{"codex", "resume"}) {
		t.Errorf("codex shared: got %q, want its picker", argv)
	}
	g := session.Restore{ID: "G", Fg: "gemini", FgArgs: []string{"gemini"}, Cwd: "/w"}
	h := session.Restore{ID: "H", Fg: "gemini", FgArgs: []string{"gemini"}, Cwd: "/w"}
	if argv, note := resumePlan(g, []session.Restore{g, h}); argv != nil || !strings.Contains(note, "--list-sessions") {
		t.Errorf("gemini shared: got %q %q, want nothing started and how to find it", argv, note)
	}
}
