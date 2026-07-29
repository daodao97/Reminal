// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// winHelperFPS is the frame-rate ceiling handed to the reminal-capture helper.
// 30fps is buttery for a window mirror and bounds host CPU (each frame is a
// ~7ms hardware JPEG encode; the helper only produces on actual change). The WS
// relay path is capped far below this independently — see wsFrameMinInterval.
const winHelperFPS = 30

// winCaptureQuality is the JPEG quality (0-100) the helper encodes at, matching
// the old sips path so frame sizes and looks are unchanged.
const winCaptureQuality = 45

// helperStartupGrace bounds how long startWinHelper waits to see whether the
// helper dies immediately (missing Screen Recording permission, window gone).
// SCShareableContent rejects fast, so a helper still alive after this is healthy.
const helperStartupGrace = 700 * time.Millisecond

// winHelper streams one window's JPEG frames from the reminal-capture helper
// process. It keeps only the newest frame (a mirror only ever wants the latest
// picture) and signals next() when one arrives.
type winHelper struct {
	cmd *exec.Cmd
	// stdin is held open (never written) purely as a lifeline: the helper exits
	// on stdin EOF, so it can't outlive the agent even when the agent dies
	// without running defers — SIGKILL, a crash, or the hot-restart syscall.Exec
	// (which closes this fd via CLOEXEC). Without it, a helper on a static
	// window would linger forever: it only notices a dead peer when a frame
	// WRITE fails, and a static window never writes.
	stdin io.WriteCloser

	mu     sync.Mutex
	latest []byte // newest frame not yet consumed by next(); nil once taken

	sig    chan struct{} // buffered(1): coalesced "new frame available"
	dead   chan struct{} // closed when the reader loop ends (process/pipe gone)
	stderr *bytes.Buffer
}

// captureHelperPath finds the reminal-capture binary: an explicit override
// first (handy for local testing), then next to the running reminal binary
// (how releases bundle it), then PATH. macOS-only — the helper needs
// ScreenCaptureKit; other platforms fall back to the screencapture path.
func captureHelperPath() (string, error) {
	if runtime.GOOS != "darwin" {
		return "", errors.New("capture helper is macOS-only")
	}
	if p := strings.TrimSpace(os.Getenv("REMINAL_CAPTURE_HELPER")); p != "" {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
		return "", fmt.Errorf("REMINAL_CAPTURE_HELPER=%s not found", p)
	}
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), "reminal-capture")
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return cand, nil
		}
	}
	if p, err := exec.LookPath("reminal-capture"); err == nil {
		return p, nil
	}
	return "", errors.New("reminal-capture not found")
}

// startWinHelper spawns the helper for window id and returns it once it's proven
// healthy (survived the startup grace without dying). Returns an error — with
// the helper's stderr when it exited — so the caller falls back to screencapture.
func startWinHelper(id string, maxWidth, quality, fps int) (*winHelper, error) {
	path, err := captureHelperPath()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(path, id, strconv.Itoa(maxWidth), strconv.Itoa(quality), strconv.Itoa(fps))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stdin, err := cmd.StdinPipe() // lifeline — see winHelper.stdin
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, err
	}
	h := &winHelper{
		cmd:    cmd,
		stdin:  stdin,
		sig:    make(chan struct{}, 1),
		dead:   make(chan struct{}),
		stderr: &stderr,
	}
	go h.readLoop(stdout)

	// Watch for early death (permission denied / window not found exit within a
	// few hundred ms). Survive the grace window → healthy.
	select {
	case <-h.dead:
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = "capture helper exited immediately"
		}
		return nil, errors.New(msg)
	case <-time.After(helperStartupGrace):
		return h, nil
	}
}

// readLoop reads [uint32 big-endian length][JPEG] frames off the helper's
// stdout, keeping only the newest, until the pipe closes.
func (h *winHelper) readLoop(stdout io.Reader) {
	defer close(h.dead)
	r := bufio.NewReaderSize(stdout, 512*1024)
	var lenBuf [4]byte
	for {
		if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
			return
		}
		n := binary.BigEndian.Uint32(lenBuf[:])
		// Sanity bound: a 1100px JPEG is tens of KB; anything past 16 MiB is a
		// framing desync, not a frame.
		if n == 0 || n > 16*1024*1024 {
			return
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			return
		}
		h.mu.Lock()
		h.latest = buf
		h.mu.Unlock()
		select {
		case h.sig <- struct{}{}:
		default: // a pending signal already covers this newer frame
		}
	}
}

// next returns the newest unsent frame, blocking up to timeout for a fresh one.
// ok is false on timeout (no new frame — the window was static this interval),
// on stop, or when the helper died. A returned frame is always new content: the
// helper only emits frames whose picture actually changed.
func (h *winHelper) next(stop <-chan struct{}, timeout time.Duration) (img []byte, ok bool) {
	select {
	case <-h.sig:
		h.mu.Lock()
		img, h.latest = h.latest, nil
		h.mu.Unlock()
		return img, img != nil
	case <-time.After(timeout):
		return nil, false
	case <-stop:
		return nil, false
	case <-h.dead:
		return nil, false
	}
}

// alive reports whether the helper process/reader is still running.
func (h *winHelper) alive() bool {
	select {
	case <-h.dead:
		return false
	default:
		return true
	}
}

// stop kills the helper process and waits for it to reap. Closing stdin first
// gives it the graceful EOF exit; the Kill is the backstop.
func (h *winHelper) stop() {
	_ = h.stdin.Close()
	if h.cmd.Process != nil {
		_ = h.cmd.Process.Kill()
	}
	_ = h.cmd.Wait()
}
