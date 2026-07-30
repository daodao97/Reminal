// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
	"github.com/reminal/reminal/internal/keepawake"
	"github.com/reminal/reminal/internal/protocol"
)

// Window mirroring lets a viewer list the host's on-screen windows, stream a
// chosen one as periodic JPEG frames, and inject mouse/keyboard input to
// control it — a single-window remote desktop layered on the existing
// end-to-end-encrypted session. Everything OS-specific lives behind
// windowBackend; the streaming + dispatch machinery on the Agent is
// platform-neutral, so a second backend (Linux/X11, Wayland) drops in via
// newWindowBackend without touching agent.go.

// winInfo describes one on-screen window on the host. It is serialized to the
// viewer verbatim (the browser renders the dropdown from these fields), so
// keep it JSON-clean. ID is an opaque, backend-defined handle the viewer
// echoes back in window_ctl / window_input — on macOS it encodes the owning
// process name and the window's index; on X11 it's the numeric window id.
type winInfo struct {
	ID    string `json:"id"`
	App   string `json:"app"`
	Title string `json:"title"`
	X     int    `json:"x"`
	Y     int    `json:"y"`
	W     int    `json:"w"`
	H     int    `json:"h"`
	// PID is the owning process id (macOS), used to activate the app reliably
	// via NSRunningApplication. Not sent to the viewer.
	PID int `json:"-"`
	// CropL/CropT are the left/top invisible CSD shadow margins to crop off the
	// captured image (Linux). X/Y/W/H already describe the content rect inside
	// that shadow, so cropping by these offsets yields a borderless frame whose
	// pixels line up 1:1 with the coordinates we map clicks against. Zero for
	// windows without a shadow and on macOS. Not sent to the viewer.
	CropL int `json:"-"`
	CropT int `json:"-"`
}

// winMenuState snapshots a window's screen bounds at the moment a right-click
// opened a context menu, plus when to stop region-capturing. See Agent.winMenu.
type winMenuState struct {
	x, y, w, h int
	until      time.Time
}

// windowBackend abstracts the OS-specific bits of window mirroring. macOS is
// implemented today (osascript + screencapture); Linux/X11 has a best-effort
// implementation via wmctrl + xdotool + ImageMagick. Wayland and Windows can
// be added as further backends behind newWindowBackend.
type windowBackend interface {
	// list enumerates the host's on-screen windows.
	list() ([]winInfo, error)
	// capture returns a JPEG of the given window's current pixels.
	capture(w winInfo) ([]byte, error)
	// captureRegion returns a JPEG of a raw screen rectangle (top-left origin,
	// screen points). Unlike capture (one window by id), it grabs whatever is
	// on screen there — used briefly after a right-click so the context menu,
	// which the OS draws as a separate window overlapping this one, is included.
	captureRegion(x, y, w, h int) ([]byte, error)
	// focus raises the window to the front so captures aren't occluded and
	// injected input lands on it.
	focus(w winInfo) error
	// clickN injects a click at (fx, fy) — fractions in 0..1 from the window's
	// top-left — with the given click-state (1=single, 2=double, 3=triple) and
	// button (right=true for the secondary/context button). The viewer reports
	// the click count so apps see native single/double/triple-clicks regardless
	// of network jitter; count falls back to the Agent's own rapid-click timer
	// for older viewers that don't send it.
	clickN(w winInfo, fx, fy float64, count int, right bool) error
	// drag presses at pts[0], drags through the intermediate points, and
	// releases at the last — a real click-drag (text selection, sliders,
	// dragging files). Points are (fx, fy) fractions in 0..1.
	drag(w winInfo, pts [][2]float64) error
	// scroll injects a scroll-wheel gesture at (fx, fy) — fractions in 0..1 —
	// by dx/dy pixel-ish deltas (positive dy scrolls the content down, matching
	// a mouse wheel / a two-finger swipe up).
	scroll(w winInfo, fx, fy, dx, dy float64) error
	// typeText types literal text into the focused window.
	typeText(w winInfo, text string) error
	// key sends a single named special key ("return", "tab", "escape",
	// "delete", "up"/"down"/"left"/"right") to the focused window.
	key(w winInfo, name string) error
	// exists reports whether a window id is still open, checking the FULL
	// window list (all Spaces, including minimized/off-screen) so a minimize
	// or Space switch isn't mistaken for a close. Returns true on error, to
	// avoid closing a pane over a transient failure.
	exists(id string) bool
	// releaseInput releases any held mouse button and modifier keys, so an
	// interrupted click/drag can never leave the host's desktop stuck in a
	// grab. Called when a pane's stream stops and when a viewer disconnects.
	releaseInput() error
	// unsupported returns a human-readable reason if this backend can't run
	// here (missing tools, wrong session type), or "" if it's usable.
	unsupported() string
	// permissionHint returns a human-readable warning if windows can be listed
	// but likely can't be captured (e.g. macOS Screen Recording permission is
	// off), or "" when capture should work. Surfaced to the viewer so a blank
	// pane comes with an explanation and a fix instead of a silent freeze.
	permissionHint() string
	// listApps enumerates launchable installed applications, so the viewer can
	// offer an app launcher (open an app, then mirror its window).
	listApps() ([]appInfo, error)
	// openApp launches (or foregrounds) the app with the given id — a .app path
	// on macOS, a .desktop file path on Linux — bringing up its window.
	openApp(id string) error
}

// appInfo describes one launchable installed application. ID is an opaque,
// backend-defined handle the viewer echoes back in app_open (a bundle path on
// macOS, a .desktop file path on Linux); Name is the display label.
type appInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// newWindowBackend picks the backend for the current OS. Unknown platforms get
// a stub whose every call reports that window mirroring isn't supported yet.
func newWindowBackend() windowBackend {
	switch runtime.GOOS {
	case "darwin":
		return darwinWindows{}
	case "linux":
		return linuxWindows{}
	default:
		return stubWindows{os: runtime.GOOS}
	}
}

// findWindow returns the window with the given ID from a fresh enumeration, so
// callers always act on current bounds (windows move and resize between the
// list the viewer saw and the moment it clicks).
func findWindow(b windowBackend, id string) (winInfo, error) {
	wins, err := b.list()
	if err != nil {
		return winInfo{}, err
	}
	for _, w := range wins {
		if w.ID == id {
			return w, nil
		}
	}
	return winInfo{}, fmt.Errorf("window no longer open")
}

// ---- Agent wiring -----------------------------------------------------------

// windows lazily constructs (once) the backend for this host and starts the
// serialized worker that drains winOps.
func (a *Agent) windows() windowBackend {
	a.winOnce.Do(func() {
		a.winBackend = newWindowBackend()
		a.winOps = make(chan func(), 64)
		go func() {
			for op := range a.winOps {
				op()
			}
		}()
	})
	return a.winBackend
}

// enqueueWinOp schedules a window operation (list / ctl / input) to run on the
// single worker goroutine, off the relay reader. Drops the op if the queue is
// full rather than blocking the reader — a lost keystroke beats a frozen
// terminal, and the viewer can retry.
func (a *Agent) enqueueWinOp(op func()) {
	a.windows() // ensure backend + worker exist
	select {
	case a.winOps <- op:
	default:
	}
}

// handleWindowList enumerates the host's windows and sends the encrypted list
// back to viewers in reply to a window_list request.
func (a *Agent) handleWindowList(conn *websocket.Conn) {
	b := a.windows()
	var payload struct {
		Windows     []winInfo `json:"windows"`
		Unsupported string    `json:"unsupported,omitempty"`
		Error       string    `json:"error,omitempty"`
		Hint        string    `json:"hint,omitempty"`
	}
	if reason := b.unsupported(); reason != "" {
		payload.Unsupported = reason
	} else if wins, err := b.list(); err != nil {
		payload.Error = err.Error()
	} else {
		payload.Windows = wins
		payload.Hint = b.permissionHint() // e.g. Screen Recording is off
	}
	a.sendWindowMsg(conn, protocol.TypeWindowList, payload)
}

// handleAppList enumerates launchable installed apps and sends the encrypted
// list back in reply to an app_list request, so the viewer can offer a launcher.
func (a *Agent) handleAppList(conn *websocket.Conn) {
	b := a.windows()
	var payload struct {
		Apps        []appInfo `json:"apps"`
		Unsupported string    `json:"unsupported,omitempty"`
		Error       string    `json:"error,omitempty"`
	}
	if reason := b.unsupported(); reason != "" {
		payload.Unsupported = reason
	} else if apps, err := b.listApps(); err != nil {
		payload.Error = err.Error()
	} else {
		payload.Apps = apps
	}
	a.sendWindowMsg(conn, protocol.TypeAppList, payload)
}

// handleAppOpen launches (or foregrounds) the app the viewer picked; its window
// then shows up on the next window_list. Best-effort — a bad id just no-ops.
func (a *Agent) handleAppOpen(encData string) {
	plaintext, err := a.box.Decrypt(encData)
	if err != nil {
		return
	}
	var ev struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(plaintext, &ev) != nil || ev.ID == "" {
		return
	}
	b := a.windows()
	if b.unsupported() != "" {
		return
	}
	_ = b.openApp(ev.ID)
}

// handleWindowCtl starts or stops streaming a window in response to a
// window_ctl request from a viewer.
func (a *Agent) handleWindowCtl(encData string) {
	plaintext, err := a.box.Decrypt(encData)
	if err != nil {
		return
	}
	var req struct {
		Action string `json:"action"`
		ID     string `json:"id"`
	}
	if json.Unmarshal(plaintext, &req) != nil {
		return
	}
	switch req.Action {
	case "start":
		a.startWindowStream(req.ID)
	case "stop":
		a.stopWindowStream(req.ID)
		_ = a.windows().releaseInput() // never leave a button held after a pane closes
	}
}

// handleWindowInput injects a mouse/keyboard event into the target window.
func (a *Agent) handleWindowInput(encData string) {
	plaintext, err := a.box.Decrypt(encData)
	if err != nil {
		return
	}
	var ev struct {
		ID      string       `json:"id"`
		Kind    string       `json:"kind"`
		X       float64      `json:"x"`
		Y       float64      `json:"y"`
		Dx      float64      `json:"dx"`
		Dy      float64      `json:"dy"`
		Path    [][2]float64 `json:"path"`
		Text    string       `json:"text"`
		Special string       `json:"special"`
		Button  string       `json:"button"` // "right" for the secondary button; else left
		Count   int          `json:"count"`  // viewer-reported click count (1/2/3); 0 = unset
	}
	if json.Unmarshal(plaintext, &ev) != nil {
		return
	}
	b := a.windows()
	if b.unsupported() != "" {
		return
	}
	w, err := findWindow(b, ev.ID)
	if err != nil {
		return
	}
	switch ev.Kind {
	case "click":
		// Raise the specific window so the click lands on it, then post a click
		// whose click-state gives native single/double/triple-click behaviour.
		// Prefer the viewer's count (it times taps at the source, immune to
		// network jitter); fall back to the Agent's own rapid-click timer for
		// older viewers that don't report it. button:"right" → context click.
		count := ev.Count
		if count < 1 {
			count = a.clickCount(w, ev.X, ev.Y)
		}
		right := ev.Button == "right"
		_ = b.focus(w)
		_ = b.clickN(w, ev.X, ev.Y, count, right)
		// A right-click opens a context menu, which macOS draws as its own window
		// (missed by capture-by-id). Snapshot this window's bounds and region-
		// capture it briefly so the menu shows; a left click dismisses the menu, so
		// clear the flag then and revert to the sharper per-window capture.
		a.winMu.Lock()
		if a.winMenu == nil {
			a.winMenu = map[string]winMenuState{}
		}
		if right {
			a.winMenu[w.ID] = winMenuState{x: w.X, y: w.Y, w: w.W, h: w.H, until: time.Now().Add(winMenuHold)}
		} else {
			delete(a.winMenu, w.ID)
		}
		a.winMu.Unlock()
	case "drag":
		_ = b.focus(w)
		_ = b.drag(w, ev.Path)
	case "scroll":
		// Raise the target window once at the start of a scroll gesture so the
		// scroll lands on IT (not whatever was focused). Skip the ~100ms raise on
		// subsequent events of the same gesture (a new gesture = a different
		// window, or a >400ms gap) so continuous scrolling stays smooth.
		if ev.ID != a.winScrollID || time.Since(a.winScrollAt) > 400*time.Millisecond {
			_ = b.focus(w)
		}
		a.winScrollID = ev.ID
		a.winScrollAt = time.Now()
		_ = b.scroll(w, ev.X, ev.Y, ev.Dx, ev.Dy)
	case "key":
		// Keys land on whatever's focused; the user focuses the target by
		// clicking it first, so we don't re-focus per keystroke (that would
		// add ~100ms osascript latency to every character).
		if ev.Special != "" {
			_ = b.key(w, ev.Special)
		} else if ev.Text != "" {
			_ = b.typeText(w, ev.Text)
		}
	}
}

// handleWindowAck records a viewer's ack of a rendered frame, unblocking the
// next frame for that window (see streamWindow). Best-effort: an unknown id or a
// momentarily full channel just drops, since only the newest seq matters.
// Acks arrive over WS (encrypted) or, when a WebRTC DataChannel is up, over that
// channel as plaintext JSON — both funnel into deliverWindowAck.
func (a *Agent) handleWindowAck(encData string) {
	plaintext, err := a.box.Decrypt(encData)
	if err != nil {
		return
	}
	var ev struct {
		ID  string `json:"id"`
		Seq uint64 `json:"seq"`
	}
	if json.Unmarshal(plaintext, &ev) != nil {
		return
	}
	a.deliverWindowAck(ev.ID, ev.Seq)
}

// deliverWindowAck feeds a decoded (id, seq) ack to the window's pacing channel.
func (a *Agent) deliverWindowAck(id string, seq uint64) {
	a.winMu.Lock()
	ch := a.winAck[id]
	a.winMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- seq:
	default:
		// Full — drop the oldest so the newest seq still gets through.
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- seq:
		default:
		}
	}
}

// clickCount returns the click-state (1=single, 2=double, 3=triple) for a click
// at (fx, fy) in window w, by timing it against the previous click. Mirrors how
// the OS coalesces rapid clicks in one spot. Runs on the winOps worker only.
func (a *Agent) clickCount(w winInfo, fx, fy float64) int {
	x := w.X + int(fx*float64(w.W))
	y := w.Y + int(fy*float64(w.H))
	now := time.Now()
	if now.Sub(a.winLastClickAt) < 450*time.Millisecond &&
		absInt(x-a.winLastClickX) <= 4 && absInt(y-a.winLastClickY) <= 4 {
		a.winClickN++
		if a.winClickN > 3 {
			a.winClickN = 1 // wrap after triple; further clicks start fresh
		}
	} else {
		a.winClickN = 1
	}
	a.winLastClickAt, a.winLastClickX, a.winLastClickY = now, x, y
	return a.winClickN
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// startWindowStream launches a capture goroutine for the given window unless
// one is already running for it. Multiple windows can stream concurrently.
func (a *Agent) startWindowStream(id string) {
	b := a.windows()
	if b.unsupported() != "" {
		return
	}
	w, err := findWindow(b, id)
	if err != nil {
		return
	}
	a.winMu.Lock()
	if a.winStreams == nil {
		a.winStreams = map[string]chan struct{}{}
		a.winAck = map[string]chan uint64{}
	}
	if _, ok := a.winStreams[id]; ok {
		a.winMu.Unlock() // already streaming this window
		return
	}
	stop := make(chan struct{})
	// Buffered so an incoming ack never blocks the reader goroutine; streamWindow
	// only cares about the newest seq, so a slot or two is plenty.
	ack := make(chan uint64, 4)
	a.winStreams[id] = stop
	a.winAck[id] = ack
	// First window under mirror → keep the display awake so the host can't
	// idle-lock and strand remote control (see winAwake).
	if a.winAwake == nil {
		a.winAwake = keepawake.StartDisplay()
	}
	a.winMu.Unlock()

	go a.streamWindow(w, stop, ack)
}

// stopWindowStream ends the stream for one window id (its pane was closed).
// An empty id stops every stream (viewer left / connection dropped).
func (a *Agent) stopWindowStream(id string) {
	a.winMu.Lock()
	if id == "" {
		for k, ch := range a.winStreams {
			close(ch)
			delete(a.winStreams, k)
		}
		a.winAck = map[string]chan uint64{}
	} else if ch, ok := a.winStreams[id]; ok {
		close(ch)
		delete(a.winStreams, id)
		delete(a.winAck, id)
	}
	// Last window stopped → let the display sleep/lock again. Capture and run the
	// stop func outside the lock (it kills+waits a child process).
	var release func()
	if len(a.winStreams) == 0 && a.winAwake != nil {
		release, a.winAwake = a.winAwake, nil
	}
	a.winMu.Unlock()
	if release != nil {
		release()
	}
}

// setStayUnlocked holds or releases a session-wide display-sleep inhibitor so
// the host can't idle-lock (see keepawake.StartDisplay). Driven by the "always
// unlocked" setting/env at startup and toggled live by the stayunlock control
// command. Idempotent.
func (a *Agent) setStayUnlocked(on bool) {
	a.stayMu.Lock()
	defer a.stayMu.Unlock()
	if on && a.stayAwake == nil {
		a.stayAwake = keepawake.StartDisplay()
	} else if !on && a.stayAwake != nil {
		a.stayAwake()
		a.stayAwake = nil
	}
}

// windowFrameInterval is a floor on the frame period — a cheap-to-capture window
// with instant acks could otherwise spin a core respawning the capture tool. The
// real pacing is ack-driven (see streamWindow), so this only caps the ceiling at
// ~16 fps; the usual capture cost (import/screencapture ~130ms) sits well above
// it, making the floor a no-op in practice.
const windowFrameInterval = 60 * time.Millisecond

// ackWaitTimeout bounds how long streamWindow waits for a frame ack before
// proceeding anyway. It keeps the stream alive if an ack is lost or the viewer
// is an older build that never acks (it just runs slower), and lets a
// backgrounded viewer trickle instead of freezing.
const ackWaitTimeout = 800 * time.Millisecond

// maxFramesInFlight caps how many sent-but-unacked frames a stream allows. 1
// would be strict lock-step (every frame waits a full round-trip, serializing
// capture and network); 2 lets the next capture overlap the previous frame's
// ack round-trip, so throughput stays near the capture rate while latency is
// still bounded — it can't accumulate past this many frames on a slow link.
const maxFramesInFlight = 2

// wsFrameMinInterval caps the window-frame rate on the BILLED WebSocket relay
// path, independent of the peer-to-peer DataChannel rate. The native capture
// helper can push ~30fps, and P2P viewers get all of it for free — but every WS
// frame is a paid relay message, so a WS-only viewer (P2P didn't connect) is
// throttled to ~5fps to keep Cloudflare cost bounded no matter how fast the host
// captures. The heartbeat isn't capped (it's tiny and only every few seconds).
const wsFrameMinInterval = 200 * time.Millisecond

// streamAckIdleTimeout stops a stream whose viewer was acking frames but then
// went silent this long. An unclean viewer drop (phone asleep, network lost,
// laptop closed) sends no WebSocket close, so the relay can be slow to tell the
// agent count==0 — meanwhile we'd keep capturing and sending frames to a viewer
// that isn't there, burning relay requests with nobody watching. Only checked
// while we're actively sending frames (an idle window sends none and expects no
// acks); a returning viewer re-sends window_ctl start. Only armed once the
// viewer has acked at least once, so a hypothetical non-acking client is never
// cut off.
const streamAckIdleTimeout = 12 * time.Second

// Change-detection knobs. A captured frame is reduced to a winSigN×winSigN
// grid of per-cell averages across THREE channels — luma (Y) and both chroma
// planes (Cb, Cr) — box-averaged so localized noise averages out; if no cell
// moved by winSigThreshold or more in ANY channel since the last SENT frame,
// the window is treated as unchanged and the frame is skipped — sparing the
// relay a frame message and its ack (each is a billed Durable Object request).
// Chroma matters: a colour-only change (a status dot green→red, syntax
// recolouring) can leave luma nearly constant, so a luma-only signature would
// miss it entirely and freeze the pane until something else forced a frame.
// The grid is deliberately fine (winSigN cells across ~1100px ≈ 23px/cell) so a
// small localized edit — a few characters, a caret, a spinner — occupies a big
// enough fraction of its cell to shift that cell's average past the threshold,
// instead of being diluted to nothing by averaging over a large block. So we
// only ever skip visually identical frames and don't drop small/colour changes.
// An unchanged window
// sends NO frames at all (0 fps); winHeartbeat only governs a tiny liveness
// ping (see streamWindow) so the viewer knows the host is alive without a frame.
// winProbeFrame is the one exception: while a DataChannel is open but not yet
// confirmed to carry frames, we send a real frame this often even if nothing
// changed — otherwise a static window would never give the channel a frame to
// prove itself and P2P would never engage. Once confirmed, it's back to 0 fps.
const (
	winSigN         = 48
	winSigThreshold = 12
	winHeartbeat    = 3 * time.Second
	winProbeFrame   = 1 * time.Second
	// winMenuHold is how long after a right-click we region-capture a window so
	// its context menu shows. Long enough to read/aim at a menu; a left click
	// (dismiss or item-select) ends it sooner. See Agent.winMenu.
	winMenuHold = 8 * time.Second
	// winLiveCheck is how often, while a mirrored window's picture is static, we
	// re-verify it's still open (see streamWindow). Bounds how long a closed
	// window can sit frozen before its pane is dropped, without spending an
	// existence check on every idle frame.
	winLiveCheck = 1200 * time.Millisecond
)

// frameSig is a coarse per-cell average of a frame across the luma (Y) and both
// chroma (Cb, Cr) planes. Keeping chroma is what lets sigDiffers catch a change
// that shifts colour but not brightness (green→red status dot, recoloured text)
// — a luma-only signature is blind to those and would freeze the pane.
type frameSig struct {
	y, cb, cr [winSigN * winSigN]byte
}

// frameSignature reduces a JPEG frame to a coarse frameSig by box-averaging each
// cell. It reads the Y/Cb/Cr planes directly for JPEG's native YCbCr (chroma is
// subsampled, so several luma pixels share a chroma sample — fine for a coarse
// average); a non-YCbCr image falls back to a per-pixel RGB→YCbCr conversion. ok
// is false when the frame can't be decoded, in which case the caller treats the
// frame as changed (always sends).
func frameSignature(jpegBytes []byte) (sig frameSig, ok bool) {
	img, err := jpeg.Decode(bytes.NewReader(jpegBytes))
	if err != nil {
		return sig, false
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return sig, false
	}
	var sumY, sumCb, sumCr [winSigN * winSigN]uint64
	var cnt [winSigN * winSigN]uint32
	if yc, isYCbCr := img.(*image.YCbCr); isYCbCr {
		for y := 0; y < h; y++ {
			cy := y * winSigN / h
			for x := 0; x < w; x++ {
				idx := cy*winSigN + x*winSigN/w
				sumY[idx] += uint64(yc.Y[yc.YOffset(b.Min.X+x, b.Min.Y+y)])
				co := yc.COffset(b.Min.X+x, b.Min.Y+y)
				sumCb[idx] += uint64(yc.Cb[co])
				sumCr[idx] += uint64(yc.Cr[co])
				cnt[idx]++
			}
		}
	} else {
		for y := 0; y < h; y++ {
			cy := y * winSigN / h
			for x := 0; x < w; x++ {
				r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
				yy, cb, cr := color.RGBToYCbCr(uint8(r>>8), uint8(g>>8), uint8(bl>>8))
				idx := cy*winSigN + x*winSigN/w
				sumY[idx] += uint64(yy)
				sumCb[idx] += uint64(cb)
				sumCr[idx] += uint64(cr)
				cnt[idx]++
			}
		}
	}
	for i := range sig.y {
		if cnt[i] > 0 {
			sig.y[i] = byte(sumY[i] / uint64(cnt[i]))
			sig.cb[i] = byte(sumCb[i] / uint64(cnt[i]))
			sig.cr[i] = byte(sumCr[i] / uint64(cnt[i]))
		}
	}
	return sig, true
}

// sigDiffers reports whether any grid cell changed by at least winSigThreshold
// in ANY of the three channels (luma or either chroma) since the last signature.
func sigDiffers(a, b frameSig) bool {
	for i := range a.y {
		if absDiffByte(a.y[i], b.y[i]) >= winSigThreshold ||
			absDiffByte(a.cb[i], b.cb[i]) >= winSigThreshold ||
			absDiffByte(a.cr[i], b.cr[i]) >= winSigThreshold {
			return true
		}
	}
	return false
}

func absDiffByte(x, y byte) int {
	d := int(x) - int(y)
	if d < 0 {
		d = -d
	}
	return d
}

// streamWindow captures w and broadcasts each JPEG frame to viewers until stop
// is closed or the connection goes away. It is ack-paced: after sending a frame
// it waits for the viewer to acknowledge rendering it before capturing the next,
// so at most one frame is ever in flight. That bounds latency (it can't
// accumulate on a slow link — the classic "the lag just keeps growing" failure)
// and makes the frame rate adapt to whatever the viewer can actually consume,
// while guaranteeing every frame sent is freshly captured. On exit it clears its
// own map entry (unless already replaced) so the id can be re-streamed.
//
// wsSinkNeeded reports whether a window frame must ALSO be sent over the
// (per-message-billed) WS relay: true whenever some viewer isn't covered by a
// confirmed P2P DataChannel — none confirmed yet, an unknown/zero viewer count,
// or more viewers than confirmed channels (a WS-only viewer). Only an all-P2P
// viewer set skips WS, so P2P still keeps frames off the relay when everyone can
// use it, while a mixed set never leaves a WS-only viewer frozen.
func wsSinkNeeded(viewerCount, confirmed int) bool {
	return confirmed == 0 || viewerCount <= 0 || viewerCount > confirmed
}

// helperRetryCooldown is how long a stream waits before re-attempting the
// native capture helper after it failed to start or died. A helper failure —
// SCK transiently losing the window (moved to another Space, minimized), a
// crash — used to demote the stream to the ~5fps screencapture path silently
// and PERMANENTLY; now it just degrades until the next retry succeeds.
const helperRetryCooldown = 10 * time.Second

// winSinks is the resolved set of destinations for ONE frame — the single
// source of truth for who receives it. Every throttle (the billed-WS rate cap,
// the probe cadence) is applied during resolution, so "will anyone take this
// frame" and the actual sends can never disagree. They once could: probing
// channels passed the send gate while the probe throttle skipped the send, so
// seq inflated ~30/s with nothing delivered, the viewer's acks fell permanently
// behind, and the in-flight gate degraded a healthy stream to a few fps.
type winSinks struct {
	confirmed []*webrtc.DataChannel // proven channels — get every frame
	probe     []*webrtc.DataChannel // unproven — at most one frame per winProbeFrame
	ws        bool                  // billed relay — at most one frame per wsFrameMinInterval
}

func (s winSinks) any() bool { return len(s.confirmed) > 0 || len(s.probe) > 0 || s.ws }

// expectsAck reports whether a frame sent to these sinks should produce a
// viewer ack: true only for proven receivers (confirmed P2P, or the relay). A
// probe-only send doesn't count — the channel is unproven, and the demote logic
// must only punish silence from viewers that demonstrably received frames.
func (s winSinks) expectsAck() bool { return len(s.confirmed) > 0 || s.ws }

// winStream is the state machine for one mirrored window. One runs per
// streamed id (see startWindowStream); all fields are goroutine-local.
type winStream struct {
	a    *Agent
	b    windowBackend
	w    winInfo
	stop <-chan struct{}
	ack  <-chan uint64

	// Frame source: the native SCK helper when available (hardware capture +
	// JPEG at winHelperFPS with its own change detection), else per-frame
	// screencapture. capNative reports which produced the CURRENT frame;
	// helperErr is why the helper isn't running (stamped onto fallback frames
	// so the viewer's popover can say WHY it's on the slow path — usually a
	// locked screen or a window SCK can't see).
	helper        *winHelper
	helperRetryAt time.Time
	helperErr     string
	capNative     bool

	// Ack pacing: seq stamps sent frames, acked tracks the viewer's highest
	// confirmation. sentSinceAck counts ack-expected frames sent since the last
	// consumed ack — the demote evidence (see dispatch).
	seq, acked   uint64
	lastAck      time.Time
	gotAnyAck    bool
	sentSinceAck int

	// Change detection for the screencapture path (the helper does its own).
	// The pending signature is committed only when the frame is actually sent —
	// committing on detection would mark a skipped frame "already seen" and
	// silently drop its content.
	lastSig, pendingSig   frameSig
	haveSig, pendingSigOK bool

	// Cadence stamps.
	lastSent     time.Time // last frame OR heartbeat (paces both)
	lastWSFrame  time.Time // billed-relay rate cap
	lastProbe    time.Time // probe-frame cadence
	lastGeoCheck time.Time // window liveness / geometry poll
	lastImg      []byte    // newest frame — lets a probe go out while idle
	fails        int       // consecutive capture failures while the window exists
}

// streamWindow captures w and streams JPEG frames to viewers until stop closes
// or the connection goes away. Frames ride confirmed P2P DataChannels at the
// full capture rate and the billed WS relay at a capped rate (wsFrameMinInterval)
// for viewers without P2P; unproven channels get one probe frame a second until
// the viewer acks one over them. The stream is ack-paced: at most
// maxFramesInFlight unacknowledged frames, so latency can't accumulate on a
// slow link and the rate adapts to what the viewer actually consumes.
func (a *Agent) streamWindow(w winInfo, stop <-chan struct{}, ack <-chan uint64) {
	s := &winStream{a: a, b: a.windows(), w: w, stop: stop, ack: ack, lastGeoCheck: time.Now()}
	defer s.cleanup()
	s.run()
}

// cleanup clears the stream's map entry (unless already replaced) so the id can
// be re-streamed, drops any right-click region-capture entry, releases the
// keep-awake inhibitor when this was the last stream, and stops the helper.
func (s *winStream) cleanup() {
	if s.helper != nil {
		s.helper.stop()
	}
	a := s.a
	a.winMu.Lock()
	if ch, ok := a.winStreams[s.w.ID]; ok && ch == s.stop {
		delete(a.winStreams, s.w.ID)
		delete(a.winAck, s.w.ID)
	}
	delete(a.winMenu, s.w.ID)
	// A stream that exits on its own — window closed, viewer went silent —
	// bypasses stopWindowStream, so it must release the keep-awake inhibitor
	// itself when it's the last one; otherwise a window closing under mirror
	// pins the host display awake forever. Only release if we actually removed
	// the last stream (a replaced id leaves the map non-empty).
	var release func()
	if len(a.winStreams) == 0 && a.winAwake != nil {
		release, a.winAwake = a.winAwake, nil
	}
	a.winMu.Unlock()
	if release != nil {
		release()
	}
}

func (s *winStream) run() {
	for {
		conn := s.a.liveConn()
		if conn == nil {
			return
		}
		s.drainAcks()
		if !s.waitCapacity() {
			return
		}
		start := time.Now()
		img, err := s.capture()
		switch {
		case err != nil || (len(img) == 0 && !s.capNative):
			// No pixels from the subprocess path: closed window, or capture
			// genuinely broken (permissions). The helper path never lands here —
			// an empty helper read just means "no change this interval".
			if !s.captureFailure(conn, err) {
				return
			}
		default:
			s.fails = 0
			changed := s.detectChange(img)
			if len(img) > 0 {
				s.lastImg = img
			}
			if !s.checkWindow(conn, changed) {
				return
			}
			s.dispatch(conn, changed)
		}
		// Floor the loop period so a cheap subprocess capture with instant acks
		// can't spin a core. The helper path self-paces (next blocks until SCK
		// delivers, at most winHelperFPS), so flooring it would only cap P2P.
		if !s.capNative {
			if rem := windowFrameInterval - time.Since(start); rem > 0 {
				select {
				case <-s.stop:
					return
				case <-time.After(rem):
				}
			}
		}
	}
}

// consumeAck folds one viewer ack into the pacing state.
func (s *winStream) consumeAck(v uint64) {
	if v > s.acked {
		s.acked = v
	}
	s.lastAck, s.gotAnyAck, s.sentSinceAck = time.Now(), true, 0
}

// drainAcks consumes every ack that arrived since the last iteration, without
// blocking. Draining only inside the in-flight gate (which is entered when 2+
// frames are outstanding) let lastAck go stale while the viewer was acking
// perfectly — which the demote logic then misread as a dead channel.
func (s *winStream) drainAcks() {
	for {
		select {
		case v := <-s.ack:
			s.consumeAck(v)
		default:
			return
		}
	}
}

// waitCapacity blocks while maxFramesInFlight frames are unacknowledged, so we
// never outrun the viewer by more than that — latency stays bounded instead of
// accumulating on a slow link. Returns false when the stream should end: stop
// closed, or a viewer that used to ack has been silent past
// streamAckIdleTimeout (it vanished uncleanly; don't stream to nobody).
func (s *winStream) waitCapacity() bool {
	for s.seq-s.acked >= maxFramesInFlight {
		select {
		case v := <-s.ack:
			s.consumeAck(v)
		case <-time.After(ackWaitTimeout):
			if s.gotAnyAck && time.Since(s.lastAck) > streamAckIdleTimeout {
				return false
			}
			s.acked = s.seq // assume delivered; keep the stream moving
		case <-s.stop:
			return false
		}
	}
	return true
}

// ensureHelper keeps the native capture helper alive: reaps one that died and
// (re)starts it, subject to helperRetryCooldown after a failure.
func (s *winStream) ensureHelper() {
	if os.Getenv("REMINAL_NO_CAPTURE_HELPER") != "" {
		return
	}
	if s.helper != nil {
		if s.helper.alive() {
			return
		}
		msg := strings.TrimSpace(s.helper.stderr.String())
		if msg == "" {
			msg = "capture helper exited"
		}
		s.helperErr = msg
		s.helper.stop()
		s.helper = nil
		s.helperRetryAt = time.Now().Add(helperRetryCooldown)
	}
	if time.Now().Before(s.helperRetryAt) {
		return
	}
	if h, err := startWinHelper(s.w.ID, winMaxWidth, winCaptureQuality, winHelperFPS); err == nil {
		s.helper = h
		s.helperErr = ""
	} else {
		s.helperErr = err.Error()
		s.helperRetryAt = time.Now().Add(helperRetryCooldown)
	}
}

// activeMenu returns the window's live right-click region-capture entry, if the
// hold hasn't expired (see handleWindowInput's right-click path).
func (a *Agent) activeMenu(id string) (winMenuState, bool) {
	a.winMu.Lock()
	defer a.winMu.Unlock()
	m, ok := a.winMenu[id]
	if ok && time.Now().After(m.until) {
		delete(a.winMenu, id)
		return m, false
	}
	return m, ok
}

// capture produces the next frame. Helper path: blocks up to winHeartbeat for a
// changed frame (an empty result = the window was static this interval).
// Subprocess path (helper unavailable, or a context menu needs a region grab):
// one screencapture, change-detected by the caller via frame signatures.
func (s *winStream) capture() (img []byte, err error) {
	menu, menuActive := s.a.activeMenu(s.w.ID)
	s.ensureHelper()
	if s.helper != nil && !menuActive {
		s.capNative = true
		img, _ = s.helper.next(s.stop, winHeartbeat)
		return img, nil
	}
	s.capNative = false
	if menuActive {
		return s.b.captureRegion(menu.x, menu.y, menu.w, menu.h)
	}
	return s.b.capture(s.w)
}

// detectChange reports whether img is new content. The helper only ever emits
// changed frames; the subprocess path compares coarse frame signatures.
func (s *winStream) detectChange(img []byte) bool {
	if len(img) == 0 {
		return false
	}
	if s.capNative {
		return true
	}
	sig, ok := frameSignature(img)
	s.pendingSig, s.pendingSigOK = sig, ok
	return !ok || !s.haveSig || sigDiffers(s.lastSig, sig)
}

// checkWindow verifies the mirrored window still exists (drop the pane if not)
// and, on the helper path, that its geometry hasn't changed — the helper scales
// into its start-time output size, so a resized window would render distorted
// forever. The geometry poll runs even while frames flow (that's exactly when
// resizes happen); the subprocess path polls existence only while static, as a
// closed window there keeps "capturing" its retained backing store instead of
// erroring. Returns false when the stream should end.
func (s *winStream) checkWindow(conn *websocket.Conn, changed bool) bool {
	if s.capNative {
		if time.Since(s.lastGeoCheck) < 2*winLiveCheck {
			return true
		}
		s.lastGeoCheck = time.Now()
		cur, err := findWindow(s.b, s.w.ID)
		if err != nil {
			s.a.sendWindowClosed(conn, s.w.ID)
			return false
		}
		resized := absInt(cur.W-s.w.W) > 8 || absInt(cur.H-s.w.H) > 8
		s.w = cur
		if resized && s.helper != nil {
			s.helper.stop()
			s.helper = nil
			if h, err := startWinHelper(s.w.ID, winMaxWidth, winCaptureQuality, winHelperFPS); err == nil {
				s.helper = h
			} else {
				s.helperRetryAt = time.Now().Add(helperRetryCooldown)
			}
		}
		return true
	}
	if changed {
		s.lastGeoCheck = time.Now()
		return true
	}
	if time.Since(s.lastGeoCheck) < winLiveCheck {
		return true
	}
	s.lastGeoCheck = time.Now()
	if !s.b.exists(s.w.ID) {
		s.a.sendWindowClosed(conn, s.w.ID)
		return false
	}
	return true
}

// captureFailure handles a subprocess capture that produced no pixels: a closed
// window ends the stream; anything else (usually missing Screen Recording
// permission) surfaces a message to the viewer instead of a silent frozen pane.
// Returns false when the stream should end.
func (s *winStream) captureFailure(conn *websocket.Conn, err error) bool {
	if !s.b.exists(s.w.ID) {
		s.a.sendWindowClosed(conn, s.w.ID)
		return false
	}
	s.fails++
	if s.fails == 4 || s.fails%64 == 0 {
		msg := s.b.permissionHint()
		if msg == "" {
			if err != nil {
				msg = "Couldn't capture this window: " + err.Error()
			} else {
				msg = "Couldn't capture this window."
			}
		}
		s.a.sendWindowMsg(conn, protocol.TypeWindowFrame, struct {
			ID    string `json:"id"`
			Error string `json:"error"`
		}{ID: s.w.ID, Error: msg})
	}
	return true
}

// dispatch decides what this iteration sends: a frame (when the picture changed
// and a sink will take it, or an unproven channel is due a probe), else a
// heartbeat when it's time. All send-rate decisions live in the sink resolution
// here — nowhere else.
func (s *winStream) dispatch(conn *websocket.Conn, changed bool) {
	confirmed, probing := s.a.rtcSinks()
	vc := s.a.currentViewerCount()

	// Demote confirmed channels only on EVIDENCE of a dead link: several
	// ack-expected frames sent since the last consumed ack, and sustained
	// silence. (Elapsed time alone falsely demoted after every idle pause — no
	// frames sent means no acks arrive — flapping the transport with no actual
	// network change.)
	if s.gotAnyAck && s.sentSinceAck >= 3 && time.Since(s.lastAck) > streamAckIdleTimeout/2 {
		s.a.unconfirmRTC()
		confirmed, probing = s.a.rtcSinks()
	}

	probeDue := len(probing) > 0 && time.Since(s.lastProbe) >= winProbeFrame
	wsDue := wsSinkNeeded(vc, len(confirmed)) && time.Since(s.lastWSFrame) >= wsFrameMinInterval

	var sinks winSinks
	if changed {
		sinks = winSinks{confirmed: confirmed, ws: wsDue}
		if probeDue {
			sinks.probe = probing
		}
	} else if probeDue && len(confirmed) == 0 {
		// Static window with an unproven channel: re-send the newest frame so
		// the channel can still prove itself (heartbeats aren't confirmable —
		// only a frame ack over the channel is). Without this, P2P could never
		// engage on a window that isn't changing.
		sinks = winSinks{probe: probing}
	}

	if sinks.any() && len(s.lastImg) > 0 {
		s.sendFrame(conn, sinks)
		return
	}
	// A changed frame with every sink throttled out this instant is simply
	// dropped — seq must not advance for a frame nobody receives (the viewer's
	// acks would fall behind and stall the in-flight gate); the next iteration
	// captures fresher content anyway.
	if time.Since(s.lastSent) >= winHeartbeat {
		s.sendHeartbeat(conn, confirmed, probing, vc)
	}
}

// winDCMaxMsg is the largest frame message the agent will put on a WebRTC
// DataChannel. A peer CLOSES the channel outright on any message above its
// maxMessageSize (Chrome 256 KiB; the spec mandates kill, not drop) — which
// showed up as the mirror spontaneously "falling back to relay" whenever the
// window content got busy enough to fatten a frame past the limit. The native
// helper already encodes under a byte budget; this is the hard backstop for the
// screencapture fallback path, which can't cheaply re-encode.
const winDCMaxMsg = 192 << 10

// sendFrame stamps and ships the newest frame to exactly the resolved sinks.
func (s *winStream) sendFrame(conn *websocket.Conn, sinks winSinks) {
	capSrc := "shot"
	if s.capNative {
		capSrc = "native"
	}
	capErr := ""
	if !s.capNative && s.helperErr != "" {
		// Why we're on the slow path — shown in the (i) popover. Bounded: SCK
		// error strings can ramble.
		capErr = s.helperErr
		if len(capErr) > 120 {
			capErr = capErr[:120] + "…"
		}
	}
	frame := struct {
		ID     string `json:"id"`
		W      int    `json:"w"`
		H      int    `json:"h"`
		Seq    uint64 `json:"seq"`
		Cap    string `json:"cap,omitempty"`     // capture source, shown in the (i) popover
		CapErr string `json:"cap_err,omitempty"` // why the native path is unavailable
		Img    string `json:"img"`               // base64 JPEG
	}{ID: s.w.ID, W: s.w.W, H: s.w.H, Seq: s.seq + 1, Cap: capSrc, CapErr: capErr, Img: base64.StdEncoding.EncodeToString(s.lastImg)}
	raw, err := json.Marshal(frame)
	if err != nil {
		return
	}
	if len(raw) > winDCMaxMsg {
		// Too big for any DataChannel — route around them entirely (better a
		// relay-capped frame than a killed channel), INCLUDING when every
		// viewer is on P2P: they'd freeze otherwise. Still relay-rate-capped;
		// if the WS window isn't open yet, drop without advancing seq and let
		// the next iteration carry fresher content.
		sinks.confirmed, sinks.probe = nil, nil
		sinks.ws = time.Since(s.lastWSFrame) >= wsFrameMinInterval
		if !sinks.ws {
			return
		}
	}
	s.seq++
	for _, dc := range sinks.confirmed {
		_ = dc.Send(raw)
	}
	if sinks.ws {
		s.a.sendWindowMsg(conn, protocol.TypeWindowFrame, frame)
		s.lastWSFrame = time.Now()
	}
	if len(sinks.probe) > 0 {
		for _, dc := range sinks.probe {
			_ = dc.Send(raw)
		}
		s.lastProbe = time.Now()
	}
	if sinks.expectsAck() {
		s.sentSinceAck++
	}
	if !s.capNative {
		s.lastSig, s.haveSig = s.pendingSig, s.pendingSigOK
	}
	s.lastSent = time.Now()
}

// sendHeartbeat pings viewers over every live path so an idle (0 fps) window
// doesn't read as a dead host. Not acked, and can't confirm a channel.
func (s *winStream) sendHeartbeat(conn *websocket.Conn, confirmed, probing []*webrtc.DataChannel, vc int) {
	hb := struct {
		ID string `json:"id"`
		HB bool   `json:"hb"`
	}{ID: s.w.ID, HB: true}
	raw, err := json.Marshal(hb)
	if err != nil {
		return
	}
	for _, dc := range confirmed {
		_ = dc.Send(raw)
	}
	if wsSinkNeeded(vc, len(confirmed)) {
		s.a.sendWindowMsg(conn, protocol.TypeWindowFrame, hb)
	}
	for _, dc := range probing {
		_ = dc.Send(raw)
	}
	s.lastSent = time.Now()
}

// currentViewerCount returns the relay-reported live viewer count.
func (a *Agent) currentViewerCount() int {
	a.viewerSizeMu.Lock()
	defer a.viewerSizeMu.Unlock()
	return a.viewerCount
}

// liveConn returns the current relay connection under the same lock the rest
// of the agent uses, or nil if we're not connected.
func (a *Agent) liveConn() *websocket.Conn {
	a.currentConnMu.Lock()
	defer a.currentConnMu.Unlock()
	if a.currentConn == nil {
		return nil
	}
	return a.currentConn
}

// sendWindowMsg JSON-encodes payload, encrypts it, and writes it to conn as a
// single message of the given type. Errors are swallowed — a dropped frame is
// not worth tearing down the session.
func (a *Agent) sendWindowMsg(conn *websocket.Conn, t protocol.MessageType, payload any) {
	// Callers on async paths (e.g. OnICECandidate) pass a.liveConn(), which is
	// nil while the relay connection is down — writing would nil-deref.
	if conn == nil {
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	enc, err := a.box.Encrypt(raw)
	if err != nil {
		return
	}
	_ = a.writeMsg(conn, protocol.Message{Type: t, Data: enc})
}

// sendWindowClosed tells the viewer a mirrored window is gone so it drops the
// pane (handled as a window_frame with closed=true — same channel as frames).
func (a *Agent) sendWindowClosed(conn *websocket.Conn, id string) {
	a.sendWindowMsg(conn, protocol.TypeWindowFrame, struct {
		ID     string `json:"id"`
		Closed bool   `json:"closed"`
	}{ID: id, Closed: true})
}

// ---- helpers shared by backends --------------------------------------------

// run executes name with args and returns trimmed stdout, or an error carrying
// stderr for diagnostics (permission prompts, missing tools).
func run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("%s: %s", name, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// have reports whether a command is on PATH.
func have(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// ---- stub backend (unsupported OS) -----------------------------------------

type stubWindows struct{ os string }

func (s stubWindows) unsupported() string {
	return "window mirroring isn't supported on " + s.os + " yet"
}
func (stubWindows) permissionHint() string                                   { return "" }
func (s stubWindows) list() ([]winInfo, error)                               { return nil, nil }
func (s stubWindows) capture(winInfo) ([]byte, error)                        { return nil, nil }
func (s stubWindows) captureRegion(int, int, int, int) ([]byte, error)       { return nil, nil }
func (s stubWindows) focus(winInfo) error                                    { return nil }
func (s stubWindows) clickN(winInfo, float64, float64, int, bool) error      { return nil }
func (s stubWindows) drag(winInfo, [][2]float64) error                       { return nil }
func (stubWindows) scroll(winInfo, float64, float64, float64, float64) error { return nil }
func (s stubWindows) exists(string) bool                                     { return true }
func (s stubWindows) releaseInput() error                                    { return nil }
func (s stubWindows) typeText(winInfo, string) error                         { return nil }
func (s stubWindows) key(winInfo, string) error                              { return nil }
func (s stubWindows) listApps() ([]appInfo, error)                           { return nil, nil }
func (s stubWindows) openApp(string) error                                   { return nil }

// tmpImage writes captured bytes to a temp file path with the given extension
// so tools that only emit to a file (screencapture) can be read back.
func tmpImage(ext string) (string, error) {
	f, err := os.CreateTemp("", "reminal-win-*."+ext)
	if err != nil {
		return "", err
	}
	name := f.Name()
	_ = f.Close()
	return name, nil
}
