// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build windows

package client

// Hot restart on Windows. There is no exec() to swap the process image, so
// restart is ADOPTION rather than replacement: every session's shell lives in
// a separate ConPTY-holder process (see internal/pty/holder_windows.go), and
// the agent is just a client of it. executeRestart therefore:
//
//  1. spawns the freshly-upgraded binary detached, with the session's
//     credentials and the holder's socket path in env (REMINAL_RESUME_*)
//     plus the standard loopback handshake channel,
//  2. waits for the new agent to report it has registered with the relay
//     (its AttachHolder connection supersedes ours at the holder — the shell
//     never blinks),
//  3. exits WITHOUT running defers — the active record now belongs to the
//     successor (which rewrote it under its own pid), so the usual
//     ClearActive-on-exit must not fire.
//
// The PID changes across a Windows restart (unlike Unix, where exec keeps
// it); everything that consumes the pid — active record, control socket —
// is re-derived by the successor at startup.
//
// Foreground sessions are refused: the agent's console is owned by whatever
// terminal launched it, and when the old process exits, that shell reclaims
// the console and would fight the successor for stdin. Headless sessions
// (`reminal new`, daemon-spawned) have no console and restart cleanly.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/reminal/reminal/internal/pty"
)

// Env keys shared with the Unix restart (same names, so tooling that knows
// one knows both), plus the Windows-only holder socket.
const (
	envResume          = "REMINAL_RESUME"
	envResumeSessionID = "REMINAL_RESUME_SESSION_ID"
	envResumePIN       = "REMINAL_RESUME_PIN"
	envResumePinHash   = "REMINAL_RESUME_PIN_HASH"
	envResumeToken     = "REMINAL_RESUME_TOKEN"
	envResumeStartedAt = "REMINAL_RESUME_STARTED_AT"
	// envResumePTYSock carries the ConPTY holder's socket path — the Windows
	// analogue of Unix's inherited PTY fd.
	envResumePTYSock = "REMINAL_RESUME_PTY_SOCK"
)

// LoadResumeState reconstructs a hot-restarted session in the successor
// process: reconnect to the holder, rebuild identity from env. (nil, nil)
// means fresh startup.
func LoadResumeState() (*ResumeState, error) {
	if os.Getenv(envResume) != "1" {
		return nil, nil
	}
	id := os.Getenv(envResumeSessionID)
	pin := os.Getenv(envResumePIN)
	pinHash := os.Getenv(envResumePinHash)
	token := os.Getenv(envResumeToken)
	sock := os.Getenv(envResumePTYSock)
	if id == "" || pin == "" || pinHash == "" || sock == "" {
		return nil, errors.New("resume requested but session id / pin / pin_hash / pty sock missing")
	}
	startedAtUnix, _ := strconv.ParseInt(os.Getenv(envResumeStartedAt), 10, 64)
	startedAt := time.Unix(startedAtUnix, 0)
	if startedAtUnix == 0 {
		startedAt = time.Now()
	}

	sess, err := pty.AttachHolder(sock)
	if err != nil {
		return nil, fmt.Errorf("resume: reattach pty holder: %w", err)
	}

	// Scrub the resume env so children of the new agent never see it.
	for _, k := range []string{envResume, envResumeSessionID, envResumePIN,
		envResumePinHash, envResumeToken, envResumeStartedAt, envResumePTYSock} {
		_ = os.Unsetenv(k)
	}

	return &ResumeState{
		SessionID: id,
		PIN:       pin,
		PinHash:   pinHash,
		Token:     token,
		StartedAt: startedAt,
		PTY:       sess,
		// The old agent set up a loopback handshake (prepareHandshake put the
		// address on our argv); report registration back so it knows it may
		// exit. Empty when nothing was passed — then nobody is waiting.
		HandshakeAddr: ParseHandshakeAddr(os.Args),
	}, nil
}

// executeRestart hands the session to a fresh process of the (presumably
// upgraded) on-disk binary. On success it never returns. Returns an error
// only when the handoff couldn't be completed — the current agent is then
// still fully in charge.
func (a *Agent) executeRestart() error {
	if a.localActive {
		return errors.New("hot restart isn't supported for foreground sessions on Windows — it works for background sessions (`reminal new`); for this one, exit and re-run reminal")
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate self for restart: %w", err)
	}
	sock := a.term.SockPath()
	if sock == "" {
		return errors.New("session has no pty holder — cannot hand off")
	}

	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer devnull.Close()

	cmd := exec.Command(exe)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, devnull
	cmd.Env = append(os.Environ(),
		envResume+"=1",
		envResumeSessionID+"="+a.sessionID,
		envResumePIN+"="+a.pin,
		envResumePinHash+"="+a.pinHash,
		envResumeToken+"="+a.token,
		envResumeStartedAt+"="+strconv.FormatInt(a.startedAt.Unix(), 10),
		envResumePTYSock+"="+sock,
	)
	// prepareHandshake appends --handshake-addr to argv (ignored by the
	// resume path except for ParseHandshakeAddr) and sets the detach attrs.
	recv, afterStart, err := prepareHandshake(cmd)
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		afterStart()
		return fmt.Errorf("start successor: %w", err)
	}
	afterStart()
	_ = cmd.Process.Release()

	// Wait until the successor is REGISTERED with the relay — not merely
	// started — so a broken new binary can't take the session down: if it
	// never reports, we time out and stay in charge.
	if _, err := recv(20 * time.Second); err != nil {
		return fmt.Errorf("successor didn't come up (still serving on the old binary): %w", err)
	}

	// Successor owns the session now: it holds the pty socket, has rewritten
	// the active record under its pid, and serves the relay. Tear down the
	// pieces the successor re-creates, then exit WITHOUT defers — our
	// deferred ClearActive would delete the record the successor just wrote.
	a.stopControlListener()
	os.Exit(0)
	return nil // unreachable
}
