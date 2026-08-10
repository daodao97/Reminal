// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar
//
// reminal-capture — a tiny ScreenCaptureKit helper that streams one window's
// frames as JPEGs on stdout for the reminal agent to forward to viewers.
//
// Why a separate native helper: the Go agent builds with CGO_ENABLED=0 and can't
// call ScreenCaptureKit. Shelling out to `screencapture` + `sips` per frame costs
// ~175ms/frame (two process spawns + a full-res intermediate), capping the window
// mirror at ~5fps. SCStream delivers hardware-composited, pre-scaled frames
// continuously (up to 60fps) and only when the picture actually changed, and
// ImageIO encodes JPEG on the hardware path — so per-frame cost drops to ~5-15ms.
//
// Protocol: argv = <windowID> [maxWidth] [quality 0-100]. Frames are written to
// stdout as [uint32 big-endian length][JPEG bytes], repeated. The agent already
// knows the window's logical size, so no per-frame dimensions are sent. Fatal
// problems (permission denied, window gone) print one line to stderr and exit
// non-zero, so the agent falls back to its screencapture path.

import ApplicationServices
import CoreGraphics
import CoreImage
import CoreMedia
import CoreVideo
import Foundation
import ImageIO
import ScreenCaptureKit

// ---- args ----
let args = CommandLine.arguments

// Input-injection subcommand: `reminal-capture scroll <x> <y> <dx> <dy>` moves
// the cursor to screen point (x,y) and posts a pixel scroll-wheel event there.
// Lives here (compiled) because the agent's JXA path builds a scroll event
// whose wheel deltas don't marshal — it posts as a silent no-op, so scrolling a
// mirrored window from a viewer did nothing. dx/dy use CGEvent convention
// (positive = up/left); the agent pre-negates from DOM deltas.
if args.count >= 2, args[1] == "scroll" {
    guard args.count >= 6, let x = Double(args[2]), let y = Double(args[3]),
          let dy = Int32(args[4]), let dx = Int32(args[5]) else {
        FileHandle.standardError.write(Data("usage: reminal-capture scroll <x> <y> <dy> <dx>\n".utf8))
        exit(2)
    }
    let src = CGEventSource(stateID: .hidSystemState)
    if let mv = CGEvent(mouseEventSource: src, mouseType: .mouseMoved,
                        mouseCursorPosition: CGPoint(x: x, y: y), mouseButton: .left) {
        mv.post(tap: .cghidEventTap)
    }
    // Modern macOS apps IGNORE phase-less synthetic scroll events (verified on
    // 14.8: plain wheel events in line or pixel units do nothing) — they only
    // honor trackpad-style gestures: continuous events carrying a scroll-phase
    // sequence. Post each call as a minimal gesture: began → changed → ended.
    func phasedScroll(_ phase: Int64, _ w1: Int32, _ w2: Int32) {
        guard let e = CGEvent(scrollWheelEvent2Source: src, units: .pixel,
                              wheelCount: 2, wheel1: w1, wheel2: w2, wheel3: 0) else { return }
        e.setIntegerValueField(.scrollWheelEventIsContinuous, value: 1)
        e.setIntegerValueField(.scrollWheelEventScrollPhase, value: phase)
        e.post(tap: .cghidEventTap)
    }
    phasedScroll(1, dy / 2, dx / 2) // began
    phasedScroll(2, dy - dy / 2, dx - dx / 2) // changed (remainder — full delta total)
    phasedScroll(4, 0, 0) // ended
    exit(0)
}

// Permission subcommand: `reminal-capture request` asks ScreenCaptureKit for
// shareable content purely to surface the Screen Recording (TCC) prompt, then
// reports. `reminal permissions` runs this via `open`ing the reminal.app, so the
// prompt is attributed to the bundle identity (sh.reminal) — the one grant that
// then covers the background daemon — instead of the terminal. Prints
// "granted"/"denied" and exits 0/1.
if args.count >= 2, args[1] == "request" {
    let sem = DispatchSemaphore(value: 0)
    var ok = false
    SCShareableContent.getWithCompletionHandler { content, err in
        ok = err == nil && content != nil
        sem.signal()
    }
    _ = sem.wait(timeout: .now() + 120)
    print(ok ? "granted" : "denied")
    exit(ok ? 0 : 1)
}

// Permission subcommand: `reminal-capture accessibility` surfaces the
// Accessibility (TCC) prompt — needed because injected mouse/scroll/click events
// (CGEvent → .cghidEventTap) are silently dropped without it. AXIsProcessTrusted
// WithOptions(prompt:true) shows the "…would like to control this computer" dialog
// when untrusted. Prints "granted"/"denied".
if args.count >= 2, args[1] == "accessibility" {
    let key = kAXTrustedCheckOptionPrompt.takeUnretainedValue() as String
    let trusted = AXIsProcessTrustedWithOptions([key: true] as CFDictionary)
    print(trusted ? "granted" : "denied")
    exit(trusted ? 0 : 1)
}

// Preflight subcommand: `reminal-capture check` reports Screen Recording status
// WITHOUT prompting (CGPreflightScreenCaptureAccess — native; the JXA bridge
// doesn't expose it). Prints "ok"/"no". The agent uses this to warn a viewer to
// run `reminal permissions` instead of streaming silent black frames.
if args.count >= 2, args[1] == "check" {
    print(CGPreflightScreenCaptureAccess() ? "ok" : "no")
    exit(0)
}

func die(_ msg: String) -> Never {
    FileHandle.standardError.write(Data((msg + "\n").utf8))
    exit(1)
}


// Parent lifeline: the agent holds our stdin pipe open and never writes; EOF
// means it's gone — including deaths that skip its cleanup (SIGKILL, crash, or
// its hot-restart exec, all of which close the fd). Without this, a helper on a
// STATIC window would outlive a dead agent forever: it only notices a closed
// stdout when a frame write fails, and a static window never writes. Armed only
// when stdin is a pipe, so running the helper by hand from a terminal still works.
var stdinStat = stat()
if fstat(0, &stdinStat) == 0 && (stdinStat.st_mode & S_IFMT) == S_IFIFO {
    DispatchQueue.global(qos: .utility).async {
        while true {
            guard let d = try? FileHandle.standardInput.read(upToCount: 4096), !d.isEmpty else {
                exit(0) // EOF or read error — parent is gone
            }
        }
    }
}

// Virtual display subcommand: `reminal-capture vdisplay <w> <h>` creates a
// software display of w×h points named "reminal" and keeps it alive until this
// process exits (stdin lifeline above included). Used by closed-lid mode: a
// headless Mac (lid shut, no monitor) has no display, so windows lose their
// coordinate space and capture/input die — this gives them a stable home, the
// same trick DeskPad/BetterDisplay use. Built on the private CGVirtualDisplay*
// classes, driven via the ObjC runtime: every selector is existence-checked
// first, so an OS release that changes the interface makes us exit(1) (the
// agent just doesn't get a display) rather than crash.
if args.count >= 2, args[1] == "vdisplay" {
    let vw = args.count >= 3 ? (UInt(args[2]) ?? 1920) : 1920
    let vh = args.count >= 4 ? (UInt(args[3]) ?? 1080) : 1080

    func objcNew(_ className: String) -> NSObject? {
        guard let cls = NSClassFromString(className) as? NSObject.Type else { return nil }
        return cls.init()
    }
    // KVC with an existence check so a renamed property in a future macOS makes
    // us fail cleanly instead of throwing setValue:forUndefinedKey:.
    func setIfPossible(_ obj: NSObject, _ key: String, _ value: Any?) -> Bool {
        let cap = key.prefix(1).uppercased() + key.dropFirst()
        guard obj.responds(to: NSSelectorFromString("set\(cap):")) else { return false }
        obj.setValue(value, forKey: key)
        return true
    }

    guard let desc = objcNew("CGVirtualDisplayDescriptor") else { die("vdisplay: CGVirtualDisplayDescriptor unavailable") }
    _ = setIfPossible(desc, "name", "reminal")
    guard setIfPossible(desc, "maxPixelsWide", vw), setIfPossible(desc, "maxPixelsHigh", vh) else {
        die("vdisplay: descriptor interface changed (maxPixels)")
    }
    // Physical size drives the assumed DPI; ~24" at this resolution reads sanely.
    _ = setIfPossible(desc, "sizeInMillimeters", NSValue(size: NSSize(width: 530, height: Double(vh) / Double(vw) * 530)))
    _ = setIfPossible(desc, "productID", 0x7265)
    _ = setIfPossible(desc, "vendorID", 0x6d69)
    _ = setIfPossible(desc, "serialNum", 0x0001)
    _ = setIfPossible(desc, "queue", DispatchQueue.main)

    guard let displayCls: AnyClass = NSClassFromString("CGVirtualDisplay") else { die("vdisplay: CGVirtualDisplay unavailable") }
    let initSel = NSSelectorFromString("initWithDescriptor:")
    guard class_getInstanceMethod(displayCls, initSel) != nil,
          let rawAlloc = (displayCls as AnyObject).perform(NSSelectorFromString("alloc"))?.takeUnretainedValue(),
          let display = (rawAlloc as? NSObject)?.perform(initSel, with: desc)?.takeUnretainedValue() as? NSObject
    else { die("vdisplay: CGVirtualDisplay init failed") }

    guard let modeCls: AnyClass = NSClassFromString("CGVirtualDisplayMode") else { die("vdisplay: CGVirtualDisplayMode unavailable") }
    let modeSel = NSSelectorFromString("initWithWidth:height:refreshRate:")
    guard let modeMethod = class_getInstanceMethod(modeCls, modeSel),
          let rawMode = (modeCls as AnyObject).perform(NSSelectorFromString("alloc"))?.takeUnretainedValue() as? NSObject
    else { die("vdisplay: mode interface changed") }
    typealias ModeInit = @convention(c) (NSObject, Selector, UInt, UInt, Double) -> NSObject
    let modeInit = unsafeBitCast(method_getImplementation(modeMethod), to: ModeInit.self)
    let mode = modeInit(rawMode, modeSel, vw, vh, 60.0)

    guard let settings = objcNew("CGVirtualDisplaySettings") else { die("vdisplay: CGVirtualDisplaySettings unavailable") }
    _ = setIfPossible(settings, "hiDPI", 0)
    guard setIfPossible(settings, "modes", [mode]) else { die("vdisplay: settings interface changed (modes)") }

    let applySel = NSSelectorFromString("applySettings:")
    guard let applyMethod = class_getInstanceMethod(type(of: display), applySel) else { die("vdisplay: applySettings unavailable") }
    typealias ApplyFn = @convention(c) (NSObject, Selector, NSObject) -> Bool
    let apply = unsafeBitCast(method_getImplementation(applyMethod), to: ApplyFn.self)
    guard apply(display, applySel, settings) else { die("vdisplay: applySettings failed") }

    let did = (display.value(forKey: "displayID") as? NSNumber)?.uint32Value ?? 0
    // One line to stdout so the agent can log/inspect; then hold forever. The
    // display lives exactly as long as this process (`display` is kept alive by
    // the closure reference below).
    print("vdisplay up id=\(did) \(vw)x\(vh)")
    FileHandle.standardOutput.synchronizeFile()
    withExtendedLifetime(display) { CFRunLoopRun() }
    exit(0)
}


// Target: a CGWindowID for one window, or "display:<CGDirectDisplayID>" for a
// whole desktop (the agent lists displays as pseudo-windows with that id form).
var windowID: UInt32 = 0
var displayID: UInt32 = 0
if args.count >= 2, args[1].hasPrefix("display:"), let d = UInt32(args[1].dropFirst("display:".count)) {
    displayID = d
} else if args.count >= 2, let w = UInt32(args[1]) {
    windowID = w
} else {
    FileHandle.standardError.write(Data("usage: reminal-capture <windowID|display:ID> [maxWidth] [quality] [fps]\n".utf8))
    exit(2)
}
let maxWidth = args.count >= 3 ? (Int(args[2]) ?? 1100) : 1100
let quality = args.count >= 4 ? (Double(args[3]) ?? 45) / 100.0 : 0.45
let fps = args.count >= 5 ? max(1, min(60, Int(args[4]) ?? 60)) : 60

// frameByteBudget bounds each JPEG so its base64-in-JSON envelope stays well
// under every browser's DataChannel maxMessageSize (140_000 × 1.34 + overhead
// ≈ 188 KiB < Chrome's 256 KiB). See the encode loop in FrameOutput.
let frameByteBudget = 140_000

let ciContext = CIContext(options: [.useSoftwareRenderer: false])
let stdoutHandle = FileHandle.standardOutput

// encodeJPEG compresses cg at the given quality via ImageIO's hardware path.
func encodeJPEG(_ cg: CGImage, _ quality: Double) -> Data? {
    let out = NSMutableData()
    guard let dest = CGImageDestinationCreateWithData(out, "public.jpeg" as CFString, 1, nil) else { return nil }
    CGImageDestinationAddImage(dest, cg, [kCGImageDestinationLossyCompressionQuality: quality] as CFDictionary)
    guard CGImageDestinationFinalize(dest) else { return nil }
    return out as Data
}
// Serialize writes: the sample handler queue is single-threaded here, but keep a
// dedicated lock so a future multi-output setup can't interleave frame bytes.
let writeLock = NSLock()



// ---- stream output: encode changed frames to JPEG and emit them ----
final class FrameOutput: NSObject, SCStreamOutput, SCStreamDelegate {
    func stream(_ stream: SCStream, didOutputSampleBuffer sampleBuffer: CMSampleBuffer, of type: SCStreamOutputType) {
        guard type == .screen else { return }

        // Emit only frames that actually changed. SCStream tags each buffer with a
        // status; .complete means new content, .idle/.blank mean "nothing changed"
        // — skipping those gives us native change-detection for free (no decode +
        // signature compare in Go, no wasted bandwidth on a static window).
        guard let attachments = CMSampleBufferGetSampleAttachmentsArray(sampleBuffer, createIfNecessary: false)
            as? [[SCStreamFrameInfo: Any]],
            let info = attachments.first,
            let statusRaw = info[.status] as? Int,
            let status = SCFrameStatus(rawValue: statusRaw),
            status == .complete
        else { return }

        guard let pixelBuffer = CMSampleBufferGetImageBuffer(sampleBuffer) else { return }
        let ciImage = CIImage(cvImageBuffer: pixelBuffer)
        guard let cgImage = ciContext.createCGImage(ciImage, from: ciImage.extent) else { return }

        // Encode under a byte budget: frames ride a WebRTC DataChannel as
        // base64-in-JSON (~1.34× the JPEG), and a peer CLOSES the channel on any
        // message above its maxMessageSize (Chrome 256 KiB, some browsers 64 KiB
        // — the spec says kill, not drop). A photo-heavy window at the base
        // quality can blow past that, so step quality down until the frame fits
        // — a briefly softer picture beats a dead transport.
        var q = quality
        var data = encodeJPEG(cgImage, q)
        while let d = data, d.count > frameByteBudget, q > 0.18 {
            q *= 0.65
            data = encodeJPEG(cgImage, q)
        }
        guard let jpeg = data, !jpeg.isEmpty else { return }

        var lenBE = UInt32(jpeg.count).bigEndian
        writeLock.lock()
        defer { writeLock.unlock() }
        // A closed pipe (agent stopped the stream) throws — exit quietly.
        do {
            try stdoutHandle.write(contentsOf: Data(bytes: &lenBE, count: 4))
            try stdoutHandle.write(contentsOf: jpeg)
        } catch {
            exit(0)
        }
    }

    func stream(_ stream: SCStream, didStopWithError error: Error) {
        die("stream stopped: \(error.localizedDescription)")
    }
}

// ---- locate the target (one window, or a whole display) ----
let sema = DispatchSemaphore(value: 0)
var targetWindow: SCWindow?
var targetDisplay: SCDisplay?
var contentErr: Error?
SCShareableContent.getExcludingDesktopWindows(false, onScreenWindowsOnly: true) { content, error in
    contentErr = error
    if displayID != 0 {
        targetDisplay = content?.displays.first(where: { $0.displayID == displayID })
    } else {
        targetWindow = content?.windows.first(where: { $0.windowID == windowID })
    }
    sema.signal()
}
sema.wait()
if let contentErr { die("shareable content (screen recording permission?): \(contentErr.localizedDescription)") }

let filter: SCContentFilter
let srcW: Double, srcH: Double
if displayID != 0 {
    guard let display = targetDisplay else { die("display \(displayID) not found") }
    // Whole desktop: everything on the display — windows, menu bar, cursor.
    filter = SCContentFilter(display: display, excludingWindows: [])
    srcW = Double(display.width)
    srcH = Double(display.height)
} else {
    guard let window = targetWindow else { die("window \(windowID) not found") }
    filter = SCContentFilter(desktopIndependentWindow: window)
    srcW = Double(window.frame.width)
    srcH = Double(window.frame.height)
}

// ---- configure + start capture ----
let config = SCStreamConfiguration()

// Output size in PIXELS. Scale the target to maxWidth wide (never up past its
// native retina resolution), preserving aspect — SCStream does the downscale in
// hardware, so there's no full-res intermediate to decode like the sips path.
let pointW = max(1.0, srcW)
let pointH = max(1.0, srcH)
var scale = 2.0
if #available(macOS 14.0, *) { scale = filter.pointPixelScale > 0 ? Double(filter.pointPixelScale) : 2.0 }
let nativeW = pointW * scale
let outW = min(Double(maxWidth), nativeW)
let outH = outW * (pointH / pointW)
config.width = max(2, Int(outW.rounded()))
config.height = max(2, Int(outH.rounded()))
config.minimumFrameInterval = CMTime(value: 1, timescale: CMTimeScale(fps)) // ceiling; idle frames are skipped
config.pixelFormat = kCVPixelFormatType_32BGRA
config.queueDepth = 5
config.showsCursor = true

let output = FrameOutput()
let stream = SCStream(filter: filter, configuration: config, delegate: output)
do {
    try stream.addStreamOutput(output, type: .screen, sampleHandlerQueue: DispatchQueue(label: "reminal.capture"))
} catch {
    die("addStreamOutput: \(error.localizedDescription)")
}
stream.startCapture { error in
    if let error { die("startCapture: \(error.localizedDescription)") }
}

// SCStream delivers frames on its own queue; keep the process alive until the
// agent kills it (stream stop) or the pipe closes.
CFRunLoopRun()
