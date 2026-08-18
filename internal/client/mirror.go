// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

// The mirror service (macOS). ALL window/desktop capture and input injection is
// performed by the always-on daemon (code identity sh.reminal), and every session
// — terminal or "+" — delegates to it over this fixed local socket. That way a
// single reminal.app permission grant (Screen Recording, Accessibility,
// Automation) covers every session, instead of terminal sessions being attributed
// to Terminal.app (the responsible process) and prompting for their own grants.
//
// This file is the DAEMON side (the server) plus the small client dial helpers.
// Wiring the session capture/input path to call these is done separately.

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// mirrorSockPath is the fixed (non-PID) socket the daemon serves capture + input
// on, so any local session can find it. ~/.reminal/mirror.sock.
// REMINAL_MIRROR_SOCK overrides it so a development daemon can be run beside
// the installed one instead of seizing its socket — testing a change to this
// service otherwise means every live session's capture and input abruptly
// routes through an unsigned build.
func mirrorSockPath() (string, error) {
	if p := strings.TrimSpace(os.Getenv("REMINAL_MIRROR_SOCK")); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".reminal", "mirror.sock"), nil
}

// ---- daemon side (server) --------------------------------------------------

// serveMirror runs the daemon's capture + input service until stop closes.
// Started by RunDaemon (macOS only).
func serveMirror(stop <-chan struct{}) {
	sock, err := mirrorSockPath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		return
	}
	_ = os.Remove(sock) // clear a stale socket left by a crash
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return
	}
	_ = os.Chmod(sock, 0o600)
	go func() {
		<-stop
		_ = ln.Close()
		_ = os.Remove(sock)
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-stop:
				return // listener closed on stop — the intended exit
			default:
			}
			// Any OTHER Accept error (e.g. EMFILE under fd pressure) must NOT kill
			// the always-on mirror service — capture/input would then fail with
			// "service restarting" forever until the daemon is restarted. Back off
			// briefly and keep accepting, matching http.Server.Serve's resilience.
			time.Sleep(50 * time.Millisecond)
			continue
		}
		go handleMirrorConn(conn)
	}
}

// handleMirrorConn reads one command line ("<cmd> <rest>") and dispatches.
// `capture` keeps the connection open streaming frames; `input`/`check` reply once.
func handleMirrorConn(conn net.Conn) {
	// Spawned per connection (go handleMirrorConn); a panic here would crash the
	// always-on daemon and take down capture/input for every session. Contain it.
	defer func() {
		if r := recover(); r != nil {
			recoverLog("handleMirrorConn", r)
		}
	}()
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		_ = conn.Close()
		return
	}
	cmd, rest, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " ")
	switch cmd {
	case "capture":
		mirrorServeCapture(conn, strings.Fields(rest)) // owns + closes conn
	case "captureregion":
		mirrorServeCaptureRegion(conn, strings.Fields(rest))
		_ = conn.Close()
	case "input":
		mirrorServeInput(conn, rest)
		_ = conn.Close()
	case "release":
		_ = darwinWindows{}.releaseInput() // unstick any held mouse button
		fmt.Fprintln(conn, "ok")
		_ = conn.Close()
	case "check":
		writeHelperCheck(conn, "check") // Screen Recording preflight
	case "axcheck":
		writeHelperCheck(conn, "ax-check") // Accessibility preflight
	case "autocheck":
		writeHelperCheck(conn, "auto-check") // Automation preflight
	default:
		_ = conn.Close()
	}
}

// writeHelperCheck runs the capture helper's non-prompting preflight subcommand in
// the daemon's granted (sh.reminal) context and writes "ok"/"no" back, then closes
// conn. The daemon is the ONLY place these checks report truthfully — run from a
// session (Terminal) or via prlctl (prltoolsd) the TCC identity is wrong. Powers
// the session-side polling that lets `reminal permissions` advance one grant at a
// time.
func writeHelperCheck(conn net.Conn, sub string) {
	out := "no"
	if p, e := captureHelperPath(); e == nil {
		if o, e := run(p, sub); e == nil {
			out = strings.TrimSpace(o)
		}
	}
	fmt.Fprintf(conn, "%s\n", out)
	_ = conn.Close()
}

// mirrorServeCapture streams a window's [uint32 BE len][payload] frames to conn
// by running the capture helper in the daemon's granted context. Closing conn
// stops it. An optional 5th arg selects the codec ("h264"); absent means JPEG,
// which is what pre-h264 sessions send. Falls back to a screencapture poll loop
// only when the native helper binary is absent AND the session asked for JPEG —
// an h264 request without a helper just closes the conn, so the session retries
// in jpeg mode (a permission/window failure likewise ends the stream — the
// session then reports it and `check` drives the "run reminal permissions" hint).
func mirrorServeCapture(conn net.Conn, args []string) {
	defer conn.Close()
	if len(args) < 4 {
		return
	}
	id, w, q, fps := args[0], args[1], args[2], args[3]
	codec := ""
	if len(args) >= 5 && args[4] == "h264" {
		codec = "h264"
	}
	helper, err := captureHelperPath()
	if err != nil {
		if codec != "h264" {
			mirrorScreencaptureLoop(conn, id, atoiOr(fps, 8))
		}
		return
	}
	cargs := []string{id, w, q, fps}
	if codec != "" {
		cargs = append(cargs, codec)
	}
	cmd := exec.Command(helper, cargs...)
	// Capture the helper's stderr. Without this Go wires a nil Stderr to
	// /dev/null, so the ONE line explaining why capture failed ("window 1234
	// not found", "stream stopped: …") was discarded before anything could see
	// it — every macOS capture failure reached the user as the generic
	// "capture helper exited", and the daemon log stayed empty too. Bounded,
	// because a wedged helper could otherwise spew without limit.
	errBuf := &capWriter{max: 4096}
	cmd.Stderr = errBuf
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	stdin, err := cmd.StdinPipe() // lifeline + command channel
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		if codec != "h264" {
			mirrorScreencaptureLoop(conn, id, atoiOr(fps, 8))
		}
		return
	}
	kill := func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
	// Forward session→daemon bytes into the helper's stdin: that's how "key\n"
	// (force an IDR) reaches the encoder. A Read returning error/EOF means the
	// session dropped the conn → stop the helper. Old sessions never write, so
	// this degrades to the pure lifeline it used to be.
	go func() {
		_, _ = io.Copy(stdin, conn)
		kill()
	}()
	_, _ = io.Copy(conn, stdout) // raw framed stream straight through
	kill()
	_ = cmd.Wait()
	// The stream ended. Tell the session WHY, and log it here too — this is
	// the only place the reason exists.
	if msg := strings.TrimSpace(errBuf.String()); msg != "" {
		fmt.Fprintf(os.Stderr, "reminal: capture %s ended: %s\n", id, msg)
		writeMirrorError(conn, msg)
	}
}

// capWriter keeps at most max bytes of what is written to it, dropping the
// rest. Enough to carry a helper's one-line failure without letting a
// misbehaving child grow the daemon's memory.
type capWriter struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (w *capWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if room := w.max - len(w.buf); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		w.buf = append(w.buf, p[:room]...)
	}
	return len(p), nil // always "succeed": losing log text must not kill the pipe
}

func (w *capWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.buf)
}

// writeMirrorError appends an out-of-band error frame to a capture stream:
// the reserved length sentinel, then the message. An OLD session reads the
// sentinel as an absurd frame length and simply ends its read loop — exactly
// what it did before this existed — so the addition is backward compatible.
func writeMirrorError(conn net.Conn, msg string) {
	if len(msg) > 480 {
		msg = msg[:480]
	}
	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[0:4], winErrFrameMagic)
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(msg)))
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, _ = conn.Write(hdr[:])
	_, _ = io.WriteString(conn, msg)
}

// mirrorScreencaptureLoop is the no-native-helper fallback: poll screencapture at
// fps and emit [len][JPEG] frames until the session closes the connection.
func mirrorScreencaptureLoop(conn net.Conn, id string, fps int) {
	b := darwinWindows{}
	var target winInfo
	if wins, err := b.list(); err == nil {
		for _, w := range wins {
			if w.ID == id {
				target = w
				break
			}
		}
	}
	if target.ID == "" {
		return
	}
	done := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, conn); close(done) }()
	if fps < 1 {
		fps = 1
	}
	tick := time.NewTicker(time.Second / time.Duration(fps))
	defer tick.Stop()
	var hdr [4]byte
	for {
		select {
		case <-done:
			return
		case <-tick.C:
			img, err := b.capture(target)
			if err != nil || len(img) == 0 {
				continue
			}
			binary.BigEndian.PutUint32(hdr[:], uint32(len(img)))
			if _, err := conn.Write(hdr[:]); err != nil {
				return
			}
			if _, err := conn.Write(img); err != nil {
				return
			}
		}
	}
}

// mirrorServeCaptureRegion returns one JPEG of a screen rectangle (used for the
// right-click context menu, which the OS draws as a separate overlapping window).
// Written as a single [len][JPEG] frame, like the streaming path.
func mirrorServeCaptureRegion(conn net.Conn, args []string) {
	if len(args) < 4 {
		return
	}
	img, err := darwinWindows{}.captureRegion(atoiOr(args[0], 0), atoiOr(args[1], 0), atoiOr(args[2], 0), atoiOr(args[3], 0))
	if err != nil || len(img) == 0 {
		return
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(img)))
	_, _ = conn.Write(hdr[:])
	_, _ = conn.Write(img)
}

// The daemon's record of the front window. Each input arrives on its own
// short-lived connection, so this can't live on the connection. Same rule and
// the same implementation the in-process backend uses — see frontWindowTracker,
// which explains why raising is keyed on which window is in front rather than
// on how recently the last event arrived.
var mirrorInput inputState

// mirrorServeInput injects one forwarded viewer event using the daemon's granted
// backend, then replies ok/error.
// mirrorServeInput injects one forwarded viewer event using the daemon's granted
// backend, then replies ok/error. The injection is applyWindowInput, shared with
// the in-process path so the two cannot drift apart again.
func mirrorServeInput(conn net.Conn, payload string) {
	var ev windowInput
	if json.Unmarshal([]byte(payload), &ev) != nil {
		fmt.Fprintln(conn, "error: bad event")
		return
	}
	// The daemon has no Agent to hang state on, so it keeps its own record of
	// the front window, its own run of clicks and its own drag watchdog.
	applyWindowInput(darwinWindows{}, &mirrorInput, ev, nil)
	fmt.Fprintln(conn, "ok")
}

func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

// ---- session side (client) -------------------------------------------------

// mirrorDialTimeout bounds how long a session waits to reach the daemon before
// treating screen sharing as "service restarting."
const mirrorDialTimeout = 2 * time.Second

// errMirrorUnavailable marks a capture failure that carries NO information
// about whether capture can work: the daemon was restarting, or its socket
// wasn't there yet. Callers must not read it as evidence against a codec — a
// daemon bounce lasts about a second, and treating it as "this machine cannot
// encode H.264" silently drops every open pane to JPEG for the rest of its
// life. Wrapped, so the underlying dial error is still available.
var errMirrorUnavailable = errors.New("screen-sharing service starting — retry")

// startMirrorCapture (session side) dials the daemon's mirror socket and returns
// a winHelper streaming the window's frames from the daemon (sh.reminal context).
// The helper reads the same framed format as the direct exec path, so the
// caller's streaming logic is unchanged. codec "" means jpeg; "h264" appends the
// codec token (an OLD daemon ignores it and streams JPEG — the winHelper framing
// validator catches that and ends the stream, so the caller falls back to jpeg).
// Errors when the daemon is unreachable — callers surface "screen-sharing
// service restarting" rather than falling back to a terminal-attributed capture.
func startMirrorCapture(id string, maxWidth, quality, fps int, codec string) (*winHelper, error) {
	sock, err := mirrorSockPath()
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTimeout("unix", sock, mirrorDialTimeout)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errMirrorUnavailable, err)
	}
	line := fmt.Sprintf("capture %s %d %d %d\n", id, maxWidth, quality, fps)
	if codec == "h264" {
		line = fmt.Sprintf("capture %s %d %d %d %s\n", id, maxWidth, quality, fps, codec)
	}
	if _, err := io.WriteString(conn, line); err != nil {
		_ = conn.Close()
		return nil, err
	}
	h := &winHelper{
		conn:   conn,
		codec:  codec,
		sig:    make(chan struct{}, 1),
		dead:   make(chan struct{}),
		stderr: &bytes.Buffer{},
	}
	go h.readLoop(conn)
	select {
	case <-h.dead:
		// The daemon dropped the stream before it got going (permission/window
		// gone). readLoop ended but doesn't own the conn — close it so we don't
		// leak the fd (the happy path closes it via winHelper.stop()).
		_ = conn.Close()
		if msg := h.errorText(); msg != "" {
			return nil, errors.New(msg) // the daemon told us why
		}
		return nil, errors.New("screen-sharing service closed the stream")
	case <-time.After(helperStartupGrace):
		return h, nil
	}
}

// mirrorForwardInput (session side) forwards one viewer input event (the decrypted
// JSON) to the daemon, which injects it in the granted sh.reminal context. Best
// effort — a missed click is better than blocking the input worker.
func mirrorForwardInput(eventJSON string) {
	sock, err := mirrorSockPath()
	if err != nil {
		return
	}
	conn, err := net.DialTimeout("unix", sock, mirrorDialTimeout)
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	if _, err := fmt.Fprintf(conn, "input %s\n", strings.TrimSpace(eventJSON)); err != nil {
		return
	}
	var reply [16]byte
	_, _ = conn.Read(reply[:]) // wait for ok/error so events stay ordered
}

// mirrorCheck (session side) asks the daemon whether Screen Recording is granted
// in its context. Returns "ok"/"no", or "" when the daemon is unreachable.
func mirrorCheck() string { return mirrorGrantQuery("check") }

// mirrorGrantQuery (session side) asks the daemon to run one non-prompting
// permission preflight in ITS granted (sh.reminal) context — "check" (Screen
// Recording), "axcheck" (Accessibility), or "autocheck" (Automation). Returns
// "ok"/"no", or "" when the daemon is unreachable. This is the only trustworthy
// vantage point: the same check run from a session (Terminal) or via prlctl
// (prltoolsd) reports the wrong identity. Drives `reminal permissions` polling.
func mirrorGrantQuery(cmd string) string {
	sock, err := mirrorSockPath()
	if err != nil {
		return ""
	}
	conn, err := net.DialTimeout("unix", sock, mirrorDialTimeout)
	if err != nil {
		return ""
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	if _, err := fmt.Fprintln(conn, cmd); err != nil {
		return ""
	}
	var buf [16]byte
	n, _ := conn.Read(buf[:])
	return strings.TrimSpace(string(buf[:n]))
}

// mirrorRelease (session side) asks the daemon to release any held mouse button —
// injection happens in the daemon, so a stranded press from an interrupted drag
// must be cleared there, not in this session's (Terminal) context.
func mirrorRelease() {
	sock, err := mirrorSockPath()
	if err != nil {
		return
	}
	conn, err := net.DialTimeout("unix", sock, mirrorDialTimeout)
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	if _, err := fmt.Fprintln(conn, "release"); err != nil {
		return
	}
	var reply [16]byte
	_, _ = conn.Read(reply[:])
}

// mirrorCaptureRegion (session side) fetches one region JPEG from the daemon (for
// the right-click context menu), reading a single [len][JPEG] frame.
func mirrorCaptureRegion(x, y, w, h int) ([]byte, error) {
	sock, err := mirrorSockPath()
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTimeout("unix", sock, mirrorDialTimeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	if _, err := fmt.Fprintf(conn, "captureregion %d %d %d %d\n", x, y, w, h); err != nil {
		return nil, err
	}
	var lenBuf [4]byte
	if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n == 0 || n > 16*1024*1024 {
		return nil, errors.New("bad region frame")
	}
	img := make([]byte, n)
	if _, err := io.ReadFull(conn, img); err != nil {
		return nil, err
	}
	return img, nil
}
