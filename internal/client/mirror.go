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
	"encoding/binary"
	"encoding/json"
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
			return // listener closed on stop
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
	case "input":
		mirrorServeInput(conn, rest)
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
