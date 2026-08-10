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
	"time"
)

// mirrorSockPath is the fixed (non-PID) socket the daemon serves capture + input
// on, so any local session can find it. ~/.reminal/mirror.sock.
func mirrorSockPath() (string, error) {
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
		out := "no"
		if p, e := captureHelperPath(); e == nil {
			if o, e := run(p, "check"); e == nil {
				out = strings.TrimSpace(o)
			}
		}
		fmt.Fprintf(conn, "%s\n", out)
		_ = conn.Close()
	default:
		_ = conn.Close()
	}
}

// mirrorServeCapture streams a window's [uint32 BE len][JPEG] frames to conn by
// running the capture helper in the daemon's granted context. Closing conn stops
// it. Falls back to a screencapture poll loop only when the native helper binary
// is absent (a permission/window failure just ends the stream — the session then
// reports it and the `check` command drives the "run reminal permissions" hint).
func mirrorServeCapture(conn net.Conn, args []string) {
	defer conn.Close()
	if len(args) < 4 {
		return
	}
	id, w, q, fps := args[0], args[1], args[2], args[3]
	helper, err := captureHelperPath()
	if err != nil {
		mirrorScreencaptureLoop(conn, id, atoiOr(fps, 8))
		return
	}
	cmd := exec.Command(helper, id, w, q, fps)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	stdin, err := cmd.StdinPipe() // lifeline
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		mirrorScreencaptureLoop(conn, id, atoiOr(fps, 8))
		return
	}
	kill := func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
	// A capture conn is write-only from the daemon; a Read returning means the
	// session dropped it → stop the helper.
	go func() {
		_, _ = io.Copy(io.Discard, conn)
		kill()
	}()
	_, _ = io.Copy(conn, stdout) // raw [len][JPEG] frames straight through
	kill()
	_ = cmd.Wait()
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

// mirrorInputEvent mirrors the viewer input event the session forwards verbatim.
type mirrorInputEvent struct {
	ID      string       `json:"id"`
	Kind    string       `json:"kind"`
	X       float64      `json:"x"`
	Y       float64      `json:"y"`
	Dx      float64      `json:"dx"`
	Dy      float64      `json:"dy"`
	Path    [][2]float64 `json:"path"`
	Text    string       `json:"text"`
	Special string       `json:"special"`
	Button  string       `json:"button"`
	Count   int          `json:"count"`
}

// mirrorServeInput injects one forwarded viewer event using the daemon's granted
// backend, then replies ok/error.
func mirrorServeInput(conn net.Conn, payload string) {
	var ev mirrorInputEvent
	if json.Unmarshal([]byte(payload), &ev) != nil {
		fmt.Fprintln(conn, "error: bad event")
		return
	}
	b := darwinWindows{}
	w, err := findWindow(b, ev.ID)
	if err != nil {
		fmt.Fprintln(conn, "error: window gone")
		return
	}
	switch ev.Kind {
	case "click":
		count := ev.Count
		if count < 1 {
			count = 1
		}
		_ = b.focus(w)
		_ = b.clickN(w, ev.X, ev.Y, count, ev.Button == "right")
	case "drag":
		_ = b.focus(w)
		_ = b.drag(w, ev.Path)
	case "scroll":
		_ = b.focus(w)
		_ = b.scroll(w, ev.X, ev.Y, ev.Dx, ev.Dy)
	case "key":
		if ev.Special != "" {
			_ = b.key(w, ev.Special)
		} else if ev.Text != "" {
			_ = b.typeText(w, ev.Text)
		}
	}
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

// startMirrorCapture (session side) dials the daemon's mirror socket and returns
// a winHelper streaming the window's frames from the daemon (sh.reminal context).
// The helper reads the same [len][JPEG] format as the direct exec path, so the
// caller's streaming logic is unchanged. Errors when the daemon is unreachable —
// callers surface "screen-sharing service restarting" rather than falling back to
// a terminal-attributed capture.
func startMirrorCapture(id string, maxWidth, quality, fps int) (*winHelper, error) {
	sock, err := mirrorSockPath()
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTimeout("unix", sock, mirrorDialTimeout)
	if err != nil {
		return nil, fmt.Errorf("screen-sharing service starting — retry: %w", err)
	}
	if _, err := fmt.Fprintf(conn, "capture %s %d %d %d\n", id, maxWidth, quality, fps); err != nil {
		_ = conn.Close()
		return nil, err
	}
	h := &winHelper{
		conn:   conn,
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
func mirrorCheck() string {
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
	if _, err := fmt.Fprintln(conn, "check"); err != nil {
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
