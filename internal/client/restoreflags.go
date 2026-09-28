// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"reminal/internal/config"
)

// What a coding agent was started with is kept when it is resumed — a
// person who ran `claude --dangerously-skip-permissions --model opus` gets
// that claude back, not a default one. Which arguments are flags, which of
// those take a value, and which a resume accepts at all is the agent's own
// to say: it is read from its --help at restore time, so a flag added in
// its next release is carried as correctly as one that exists today. A
// guess would be worse than nothing — a value taken for a prompt sends the
// prompt again; a prompt taken for a value does the same.

// flagSpec is what an agent's --help says about its flags: every flag it
// knows, and whether each takes a value.
type flagSpec map[string]bool

// helpArgs is how to ask an agent for the help of the command that resumes
// it — codex resumes with a subcommand of its own, with its own flags.
var helpArgs = map[string][]string{
	"codex": {"resume", "--help"},
}

var (
	ansiRe     = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	flagTokRe  = regexp.MustCompile(`^-{1,2}[A-Za-z0-9][A-Za-z0-9_-]*$`)
	yargsType  = regexp.MustCompile(`\[(string|number|array|count)\]|\[choices:`)
	yargsBool  = regexp.MustCompile(`\[boolean\]`)
	takesValue = regexp.MustCompile(`^(<[^>]*>|\[[^\]]*\]|[A-Z][A-Z0-9_]*\b)`)
)

// parseHelpFlags reads the flags out of a --help: commander (claude,
// cursor-agent, pi), clap (codex) and yargs (gemini, qwen, opencode) all
// list them one to a line, indented, as `-m, --model <MODEL>` or
// `--print, -p` or `-y, --yolo … [boolean]`. A value is a `<placeholder>`,
// an `[optional]` one, an UPPERCASE name, or a yargs type other than boolean
// anywhere in the entry.
func parseHelpFlags(help string) flagSpec {
	spec := flagSpec{}
	lines := strings.Split(ansiRe.ReplaceAllString(help, ""), "\n")
	var names []string
	value := false
	flush := func() {
		for _, n := range names {
			spec[n] = value
		}
		names, value = nil, false
	}
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "-") || t == "--" || !strings.HasPrefix(line, " ") {
			// A continuation of the entry above, where yargs may have
			// wrapped its type.
			if len(names) > 0 && t != "" {
				if yargsType.MatchString(t) {
					value = true
				}
				continue
			}
			flush()
			continue
		}
		flush()
		rest := t
		for {
			tok := rest
			if i := strings.IndexAny(rest, " ,="); i >= 0 {
				tok = rest[:i]
			}
			if !flagTokRe.MatchString(tok) {
				break
			}
			names = append(names, tok)
			rest = strings.TrimLeft(rest[len(tok):], ", =")
			if !strings.HasPrefix(rest, "-") {
				break
			}
		}
		if len(names) == 0 {
			continue
		}
		switch {
		case yargsBool.MatchString(rest):
			value = false
		case yargsType.MatchString(rest), takesValue.MatchString(rest):
			value = true
		}
	}
	flush()
	return spec
}

// agentHelp is an agent's --help, asked for through the person's own login
// shell so the agent is found where they would find it. Empty when it
// cannot be had in time.
func agentHelp(bin string) string {
	args := helpArgs[bin]
	if args == nil {
		args = []string{"--help"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// No login shell to ask; PATH (with PATHEXT's .cmd shims) finds it.
		cmd = exec.CommandContext(ctx, bin, args...)
	} else {
		cmd = exec.CommandContext(ctx, config.Shell(), "-lc", shellJoin(append([]string{bin}, args...)))
	}
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb", "CI=1")
	out, _ := cmd.CombinedOutput()
	return string(out)
}

// helpSpec is the flag spec for an agent, or nil when its help says nothing
// usable — then only the few flags known here are carried.
var helpSpec = func(bin string) flagSpec {
	s := parseHelpFlags(agentHelp(bin))
	if len(s) < 3 {
		return nil
	}
	return s
}
