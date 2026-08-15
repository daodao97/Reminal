// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build windows

package pty

// The ConPTY holder: the piece that makes hot restart possible on Windows.
//
// An HPCON is not a kernel handle — it's an opaque pointer into the creating
// process, so a pseudo console can never be handed to another process, and
// conhost tears the shell down the moment its creator exits (verified
// empirically; SetHandleInformation on an HPCON fails with "invalid handle").
// Unix hot restart survives because exec() keeps the process; Windows has no
// exec(). So on Windows the ConPTY must be owned by a process that DOESN'T
// restart: every session spawns a tiny holder (`reminal __ptyhold`, this
// file) that owns the pseudo console + shell for the session's whole life and
// serves them over a per-session AF_UNIX socket. The agent is just a client —
// replacing the agent (hot restart) is then a reconnect, and the shell never
// notices.
//
// Wire protocol, both directions, over the socket:
//
//	[type:1][len:2 LE][payload]
//
//	holder → agent:  frHello {shellPid u32, cols u16, rows u16}, then the
//	                 disconnect-window ring buffer as frData, then live frData;
//	                 frExit {code i32} when the shell ends (socket closes after).
//	agent  → holder: frData (keystrokes), frResize {cols u16, rows u16},
//	                 frKill (tear the shell down now — agent Close()).
//
// One client at a time; a new connection supersedes the old (that IS the hot
// restart). While no client is attached, output accumulates in a bounded ring
// so the restart gap loses nothing. If no client attaches for reconnectGrace,
// the holder assumes its agent was killed for good and tears down — that keeps
// `reminal kill` semantics (kill the agent ⇒ the shell dies too, just a few
// seconds later) without leaking orphan shells.

import (
	cryptorand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	frHello  = 0x01
	frData   = 0x02
	frResize = 0x03
	frExit   = 0x04
	frKill   = 0x05

	// maxFramePayload bounds one frame; shell output is chunked to fit.
	maxFramePayload = 32 * 1024

	// ringCap bounds output buffered while no agent is connected (the restart
	// window). Enough for a very chatty build log during a multi-second gap.
	ringCap = 256 * 1024

	// reconnectGrace is how long the holder tolerates having no agent before
	// concluding the agent is gone for good (killed, crashed) and tearing the
	// shell down. Hot restart reconnects within ~1s; this only has to be
	// comfortably above that.
	reconnectGrace = 15 * time.Second
)

// writeFrame emits one frame; the caller serialises via its own lock.
func writeFrame(w io.Writer, typ byte, payload []byte) error {
	hdr := [3]byte{typ, byte(len(payload)), byte(len(payload) >> 8)}
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) > 0 {
		_, err := w.Write(payload)
		return err
	}
	return nil
}

// readFrame reads one frame.
func readFrame(r io.Reader) (typ byte, payload []byte, err error) {
	var hdr [3]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := int(hdr[1]) | int(hdr[2])<<8
	payload = make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return hdr[0], payload, nil
}

// HolderSocketPath returns the AF_UNIX path a fresh holder should listen on.
func HolderSocketPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	var rnd [8]byte
	if _, err := cryptorand.Read(rnd[:]); err != nil {
		return "", err
	}
	return filepath.Join(home, ".reminal", fmt.Sprintf("pty-%x.sock", rnd)), nil
}

// RunHolder is the body of the hidden `reminal __ptyhold <sock> <shell>`
// process. It returns only when the session is over (shell exited, or the
// agent vanished past the grace window). The holder inherits the agent's
// environment, so shellEnv() composes the same shell environment the agent
// would have.
func RunHolder(sockPath, shell string) error {
	s, err := startDirect(shell)
	if err != nil {
		return err
	}
	defer s.Close()

	if err := os.MkdirAll(filepath.Dir(sockPath), 0o700); err != nil {
		return err
	}
	_ = os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return err
	}
	defer ln.Close()
	defer os.Remove(sockPath)
	_ = os.Chmod(sockPath, 0o600)

	h := &holderState{sess: s, lastDetach: time.Now()}

	// Output pump: shell → (ring while detached) + live client. Signals exit.
	shellDone := make(chan error, 1)
	go func() {
		buf := make([]byte, maxFramePayload)
		for {
			n, err := s.Read(buf)
			if n > 0 {
				h.deliver(buf[:n])
			}
			if err != nil {
				shellDone <- s.Wait() // reaper has (or will) record the exit
				return
			}
		}
	}()

	// Grace watchdog: no agent for too long ⇒ the agent was killed, not
	// restarted. Tear down so the shell can't outlive its session.
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for range t.C {
			if h.detachedLongerThan(reconnectGrace) {
				_ = s.Close() // unblocks the output pump with an error → exit
				return
			}
		}
	}()

	// Accept loop: one client at a time; new connection supersedes old.
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed — holder is exiting
			}
			h.attach(conn)
			go h.serveClient(conn)
		}
	}()

	err = <-shellDone
	// Tell whoever is attached that the shell ended, then let the process
	// exit tear everything else down.
	var code int32
	if err != nil {
		code = 1 // detail stays holder-side; the agent reports a nonzero exit
	}
	h.sendExit(code)
	return nil
}

type holderState struct {
	sess *directSession

	mu         sync.Mutex
	conn       net.Conn
	ring       []byte
	lastDetach time.Time
}

// attach makes conn the active client: supersede any old one, replay state.
func (h *holderState) attach(conn net.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conn != nil {
		_ = h.conn.Close() // superseded — the old agent is on its way out
	}
	h.conn = conn

	cols, rows, _ := h.sess.Getsize()
	var hello [8]byte
	binary.LittleEndian.PutUint32(hello[0:], uint32(h.sess.Pid()))
	binary.LittleEndian.PutUint16(hello[4:], cols)
	binary.LittleEndian.PutUint16(hello[6:], rows)
	if writeFrame(conn, frHello, hello[:]) != nil {
		_ = conn.Close()
		h.conn = nil
		return
	}
	// Replay everything the shell said while nobody was listening.
	for off := 0; off < len(h.ring); off += maxFramePayload {
		end := off + maxFramePayload
		if end > len(h.ring) {
			end = len(h.ring)
		}
		if writeFrame(conn, frData, h.ring[off:end]) != nil {
			_ = conn.Close()
			h.conn = nil
			return
		}
	}
	h.ring = nil
}

// deliver routes shell output to the live client, or the ring while detached.
func (h *holderState) deliver(b []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conn != nil {
		if writeFrame(h.conn, frData, b) == nil {
			return
		}
		// Client write failed — treat as detached and fall through to buffer.
		_ = h.conn.Close()
		h.conn = nil
		h.lastDetach = time.Now()
	}
	h.ring = append(h.ring, b...)
	if over := len(h.ring) - ringCap; over > 0 {
		h.ring = h.ring[over:] // keep the tail — the freshest output wins
	}
}

func (h *holderState) detachedLongerThan(d time.Duration) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.conn == nil && time.Since(h.lastDetach) > d
}

func (h *holderState) sendExit(code int32) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conn != nil {
		var p [4]byte
		binary.LittleEndian.PutUint32(p[:], uint32(code))
		_ = writeFrame(h.conn, frExit, p[:])
		_ = h.conn.Close()
		h.conn = nil
	}
}

// serveClient pumps agent→holder frames for one connection until it drops.
func (h *holderState) serveClient(conn net.Conn) {
	for {
		typ, payload, err := readFrame(conn)
		if err != nil {
			h.mu.Lock()
			if h.conn == conn { // we weren't superseded — genuinely detached
				h.conn = nil
				h.lastDetach = time.Now()
			}
			h.mu.Unlock()
			return
		}
		switch typ {
		case frData:
			_, _ = h.sess.Write(payload)
		case frResize:
			if len(payload) == 4 {
				cols := binary.LittleEndian.Uint16(payload[0:])
				rows := binary.LittleEndian.Uint16(payload[2:])
				_ = h.sess.Resize(cols, rows)
			}
		case frKill:
			// Agent Close(): the session is over — kill the shell now. The
			// output pump sees the teardown and drives the normal exit path.
			_ = h.sess.Close()
		}
	}
}

var errHolderGone = errors.New("pty holder is gone")
