// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"reminal/internal/session"
)

// Logical restore: a session that was running when its machine went down
// comes back as itself — the same id and PIN, so every link and every
// viewer's recents still work — with its scrollback, a new shell where the
// old one was, and the coding agent it was running started again on the
// same conversation. The processes are new; what a person was doing is not
// lost. Works on every OS: it needs nothing from the kernel.
//
// While a session runs, its agent keeps a session.Restore record and a copy
// of its scrollback up to date (restoreSaveEvery). The record survives the
// process — a shutdown, a crash, a reboot — and goes only when the session
// is ended on purpose. `reminal restore` (or the daemon, at login) starts a
// headless agent with REMINAL_RESTORE=<id>, which takes the record over.

const (
	restoreSaveEvery = 15 * time.Second
	// restoreSettle is how long a restored session's record keeps saying
	// which agent it had, while the sessions around it are restored.
	restoreSettle = 2 * time.Minute
	envRestore       = "REMINAL_RESTORE"
)

// restoreBanner marks, in the scrollback, where the old session ends and
// the restored one begins.
const restoreBanner = "\r\n\x1b[2m── reminal restored this session after its machine restarted ──\x1b[0m\r\n"

// saveRestore writes this session's restore record, and its scrollback when
// there is new output. Called on a timer; a failure costs only freshness.
func (a *Agent) saveRestore() {
	if a == nil || a.term == nil || a.paused.Load() || a.sessionID == "" {
		return
	}
	a.metaMu.Lock()
	name, cwd := a.name, a.cwd
	a.metaMu.Unlock()
	r := session.Restore{
		ID: a.sessionID, PIN: a.pin, PinHash: a.pinHash, Token: a.token,
		Name: name, Cwd: cwd, Headless: a.headless, SavedAt: time.Now(),
	}
	if pg := a.term.ForegroundPgrp(); pg > 0 && pg != a.term.Pid() {
		prog := foregroundProgram(pg, attentionForegroundName(pg))
		if _, ok := resumers[prog]; ok {
			r.Fg, r.FgArgs = prog, processArgs(pg)
			r.Conv = session.ReadConv(a.sessionID)
		}
	}
	// Something else is in the foreground for a moment (a pager the agent
	// opened, say): keep what was last known. Only the shell's own prompt
	// says the agent has ended — except just after a restore, when the
	// prompt is there because the agent has not been started again yet.
	// Sessions restored after this one decide from this record whether
	// they shared a folder with it (see resumePlan), so until the restore
	// settles it keeps saying what was running.
	restoringNow := a.restoring && time.Since(a.startedAt) < restoreSettle
	if r.Fg == "" && (restoringNow || !a.restoreFgGone()) {
		if prev, err := session.ReadRestore(a.sessionID); err == nil {
			r.Fg, r.FgArgs, r.Conv = prev.Fg, prev.FgArgs, prev.Conv
		}
	}
	_ = session.WriteRestore(r)
	if seq := a.buf.LatestSeq(); seq != a.restoreSeq {
		if p, err := session.RestoreScrollbackPath(a.sessionID); err == nil {
			if err := a.writeScrollbackDumpTo(p); err == nil {
				a.restoreSeq = seq
			}
		}
	}
}

// restoreFgGone says the shell itself is in the foreground — whatever was
// running in it has ended.
func (a *Agent) restoreFgGone() bool {
	pg := a.term.ForegroundPgrp()
	return pg > 0 && pg == a.term.Pid()
}

func (a *Agent) restoreLoop(stop <-chan struct{}) {
	t := time.NewTicker(restoreSaveEvery)
	defer t.Stop()
	a.saveRestore()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			a.saveRestore()
		}
	}
}

// settleRestore decides, as the session ends, whether it may come back. A
// session ended on purpose — its shell exited by itself — is forgotten; one
// ended by a signal (the machine shutting down, a crash) is kept. `reminal
// kill` and `reminal stop` forget it themselves.
func (a *Agent) settleRestore() {
	if a.stopSignal.Load() {
		return
	}
	if a.term != nil && a.term.EndedBySignal() {
		return
	}
	_ = session.ClearRestore(a.sessionID)
}

// ---- the command that picks the agent up again ------------------------------

type resumer struct {
	byID   func(bin string, flags []string, conv string) []string
	latest func(bin string, flags []string) []string
	// pick opens the agent's own list of conversations to choose from —
	// what is typed when "latest" could be another session's.
	pick func(bin string, flags []string) []string
	// how says, to a person, how to find a conversation again by hand, for
	// an agent with no list to open.
	how string
}

func plainFlags(bin string, flags []string, tail ...string) []string {
	return append(append([]string{bin}, flags...), tail...)
}

// resumers: how each coding agent is told to pick a conversation up again —
// by the id its hook reported when there is one, else its latest in this
// directory. The flags it was started with (a model, a permission mode)
// are kept; a prompt given on the command line is not, or it would be sent
// again.
var resumers = map[string]resumer{
	"claude": {
		byID:   func(b string, f []string, c string) []string { return plainFlags(b, f, "--resume", c) },
		latest: func(b string, f []string) []string { return plainFlags(b, f, "--continue") },
		pick:   func(b string, f []string) []string { return plainFlags(b, f, "--resume") },
	},
	"codex": {
		byID:   func(b string, _ []string, c string) []string { return []string{b, "resume", c} },
		latest: func(b string, _ []string) []string { return []string{b, "resume", "--last"} },
		pick:   func(b string, _ []string) []string { return []string{b, "resume"} },
	},
	"cursor-agent": {
		byID:   func(b string, f []string, c string) []string { return plainFlags(b, f, "--resume", c) },
		latest: func(b string, _ []string) []string { return []string{b, "resume"} },
		pick:   func(b string, f []string) []string { return plainFlags(b, f, "--resume") },
	},
	"gemini": {
		latest: func(b string, f []string) []string { return plainFlags(b, f, "--resume", "latest") },
		how:    "gemini --list-sessions, then gemini --resume <number>",
	},
	"qwen": {
		byID:   func(b string, f []string, c string) []string { return plainFlags(b, f, "--resume", c) },
		latest: func(b string, f []string) []string { return plainFlags(b, f, "--continue") },
		pick:   func(b string, f []string) []string { return plainFlags(b, f, "--resume") },
	},
	"opencode": {
		latest: func(b string, f []string) []string { return plainFlags(b, f, "--continue") },
		how:    "opencode session list, then opencode --session <id>",
	},
	"pi": {
		latest: func(b string, f []string) []string { return plainFlags(b, f, "--continue") },
		how:    "start pi and pick the conversation from its session list",
	},
	"agy": {
		latest: func(b string, f []string) []string { return plainFlags(b, f, "--continue") },
		how:    "start agy and pick the conversation from its session list",
	},
	"amp": {
		latest: func(b string, _ []string) []string { return []string{b, "threads", "continue"} },
		how:    "amp threads list, then amp threads continue <id>",
	},
}

// valueFlags take the next argument as their value. Anything else not
// starting with "-" is a positional — a prompt, most likely — and dropped.
var valueFlags = map[string]bool{
	"--model": true, "-m": true, "--permission-mode": true, "--add-dir": true, "--agent": true,
	"--settings": true, "--mcp-config": true, "--allowedTools": true, "--allowed-tools": true,
	"--disallowedTools": true, "--disallowed-tools": true, "--append-system-prompt": true,
	"--fallback-model": true, "--sandbox": true, "-s": true, "--profile": true,
	"--provider": true, "-e": true, "--extension": true, "--thinking": true, "--approval-mode": true,
}

// resumeDrop are flags that pick or start a conversation — replaced by the
// resume itself — with whether they take a value.
var resumeDrop = map[string]bool{
	"--resume": true, "-r": true, "--session-id": true, "--continue": false, "-c": false,
	"--fork-session": false,
}

// oneShot marks a run that was never interactive: nothing to resume.
var oneShot = map[string]bool{"-p": true, "--print": true, "exec": true}

// resumeArgv is the command that resumes r's agent, or nil — assuming it is
// the only one of its kind in its folder (see resumePlan).
func resumeArgv(r session.Restore) []string {
	rs, ok := resumers[r.Fg]
	if !ok {
		return nil
	}
	flags, ok := resumeFlags(r)
	if !ok {
		return nil
	}
	if r.Conv != "" && rs.byID != nil {
		return rs.byID(r.Fg, flags, r.Conv)
	}
	if rs.latest != nil {
		return rs.latest(r.Fg, flags)
	}
	return nil
}

// resumePlan is what to type into r's restored shell, and a line to show
// above it, given every other session that is being (or was) restored.
//
// Resuming by id is exact. "Latest" is exact only when r was the one
// session running its agent in its folder: when two claudes shared a
// folder and a reboot ended both, "the latest conversation here" is one
// of theirs for both of them. Then the agent's own list is opened so a
// person picks — or, for an agent with none, it is not started and the
// line says how to find the conversation.
func resumePlan(r session.Restore, peers []session.Restore) (argv []string, note string) {
	argv = resumeArgv(r)
	rs := resumers[r.Fg]
	if argv == nil || (r.Conv != "" && rs.byID != nil) || !sharesFolder(r, peers) {
		return argv, ""
	}
	flags, _ := resumeFlags(r)
	if rs.pick != nil {
		return rs.pick(r.Fg, flags), "several " + r.Fg + " conversations were running in this folder — pick this session's"
	}
	how := rs.how
	if how == "" {
		how = "start " + r.Fg + " and pick the conversation"
	}
	return nil, r.Fg + " was running here, as it was in another session in this folder; to find this one's conversation: " + how
}

// sharesFolder says another session ran the same agent in r's folder.
func sharesFolder(r session.Restore, peers []session.Restore) bool {
	for _, p := range peers {
		if p.ID != r.ID && p.Fg == r.Fg && p.Cwd != "" && filepath.Clean(p.Cwd) == filepath.Clean(r.Cwd) {
			return true
		}
	}
	return false
}

// resumeFlags are the flags r's agent was started with that a resume keeps;
// false for a run that was never interactive.
func resumeFlags(r session.Restore) ([]string, bool) {
	// Where the program's own arguments start: past an interpreter (node
	// running claude's script, say) to the argument that names it.
	args := r.FgArgs
	start := 0
	for i, a := range args {
		if strings.Contains(filepath.Base(a), r.Fg) {
			start = i + 1
			break
		}
	}
	if start == 0 && len(args) > 0 {
		start = 1
	}
	var flags []string
	for i := start; i < len(args); i++ {
		a := args[i]
		if oneShot[a] {
			return nil, false
		}
		name := a
		if k := strings.IndexByte(a, '='); k > 0 {
			name = a[:k]
		}
		if takes, drop := resumeDrop[name]; drop {
			if takes && !strings.Contains(a, "=") {
				i++
			}
			continue
		}
		if !strings.HasPrefix(a, "-") {
			continue // a prompt, or a subcommand: not carried over
		}
		flags = append(flags, a)
		if valueFlags[a] && i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return flags, true
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellJoin quotes an argv for a POSIX shell.
func shellJoin(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		if shellSafe.MatchString(a) {
			q[i] = a
		} else {
			q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return strings.Join(q, " ")
}

// ---- coming back --------------------------------------------------------------

// LoadRestoreState turns a restore record into what a headless agent starts
// from: the session's identity and scrollback, no PTY (a new shell is
// started), and the command that resumes its agent.
func LoadRestoreState(id string) (*ResumeState, string, string, error) {
	r, err := session.ReadRestore(id)
	if err != nil {
		return nil, "", "", fmt.Errorf("no restore record for %s: %w", id, err)
	}
	if r.PIN == "" {
		return nil, "", "", errors.New("restore record has no PIN")
	}
	st := &ResumeState{SessionID: r.ID, PIN: r.PIN, PinHash: r.PinHash, Token: r.Token,
		StartedAt: time.Now(), Name: r.Name, Headless: true}
	if p, err := session.RestoreScrollbackPath(r.ID); err == nil {
		st.Dump = readScrollbackDump(p)
	}
	peers, _ := session.ReadRestores()
	argv, note := resumePlan(*r, peers)
	run := ""
	if argv != nil {
		run = shellJoin(argv)
	}
	return st, run, note, nil
}

// restoreStart runs once the new shell is up: the banner, then — when an
// agent was running — the command that resumes it, typed at the prompt.
func (a *Agent) restoreStart() {
	a.record([]byte(restoreBanner))
	if a.restoreNote != "" {
		a.record([]byte("\x1b[2m" + a.restoreNote + "\x1b[0m\r\n"))
	}
	run := a.restoreRun
	if run == "" {
		return
	}
	// The prompt is drawn when the shell has printed and gone quiet.
	start := a.buf.LatestSeq()
	last, quietSince := start, time.Now()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if s := a.buf.LatestSeq(); s != last {
			last, quietSince = s, time.Now()
		}
		if last != start && time.Since(quietSince) > 700*time.Millisecond {
			break
		}
	}
	body, tail, err := PrepareInjectKeysSplit(run, true)
	if err != nil || a.injectKeys(body) != nil {
		return
	}
	if tail != nil {
		time.Sleep(EnterSettle)
		_ = a.injectKeys(tail)
	}
}

// Restorable is a session that can be brought back: a record whose session
// is not running.
func Restorable() ([]session.Restore, error) {
	all, err := session.ReadRestores()
	if err != nil {
		return nil, err
	}
	live := map[string]bool{}
	if act, err := session.ReadAllActive(); err == nil {
		for _, x := range act {
			live[x.ID] = true
		}
	}
	var out []session.Restore
	for _, r := range all {
		if !live[r.ID] {
			out = append(out, r)
		}
	}
	return out, nil
}

// RestoreSession starts a headless agent that takes r over. Its shell starts
// where the old one was, or at home when that directory is gone.
func RestoreSession(r session.Restore) (*SpawnedSession, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	defer devnull.Close()
	cmd := exec.Command(exe, "--headless")
	cmd.Env = append(os.Environ(), envRestore+"="+r.ID)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, devnull
	if dir, err := resolveSpawnDir(r.Cwd); err == nil && dir != "" {
		cmd.Dir = dir
	} else if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	recv, afterStart, err := prepareHandshake(cmd)
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		afterStart()
		return nil, err
	}
	afterStart()
	reapDetached(cmd)
	line, err := recv(spawnHandshakeTimeout)
	if err != nil {
		return nil, err
	}
	sp := &SpawnedSession{}
	if err := json.Unmarshal([]byte(line), sp); err != nil {
		return nil, fmt.Errorf("parse handshake: %w", err)
	}
	return sp, nil
}

// RestoreEnvID is the session a headless agent was started to restore, or "".
func RestoreEnvID() string { return strings.ToUpper(strings.TrimSpace(os.Getenv(envRestore))) }

// restoreAtStart brings back every session a restart ended. REMINAL_NO_RESTORE=1
// turns it off (they can still be restored by hand).
func restoreAtStart() {
	if os.Getenv("REMINAL_NO_RESTORE") == "1" {
		return
	}
	gone, err := Restorable()
	if err != nil {
		return
	}
	for _, r := range gone {
		if _, err := RestoreSession(r); err != nil {
			agentNotify("  reminal: could not restore session %s: %v\n", r.ID, err)
		}
	}
}
