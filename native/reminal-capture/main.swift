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
// Protocol: argv = <windowID> [maxWidth] [quality 0-100] [fps] [codec].
// codec "jpeg" (default): frames are written to stdout as
// [uint32 big-endian length][JPEG bytes], repeated — unchanged since v1.10, so
// an old agent driving a new helper sees the byte stream it expects.
// codec "h264": frames are [uint32 big-endian length][1 flag byte][payload]
// where length covers flag+payload, flag 1 = delta access unit, flag 2 = key
// access unit (SPS+PPS+IDR inline), payload is Annex-B H.264 from VideoToolbox
// in low-latency mode. A "key\n" line on stdin forces an immediate IDR — for a
// static window the last frame is re-encoded, so a newly attached viewer gets
// a picture without waiting for the window to change. The agent already knows
// the window's logical size, so no per-frame dimensions are sent. Fatal
// problems (permission denied, window gone) print one line to stderr and exit
// non-zero, so the agent falls back to its screencapture path (or JPEG mode).

import ApplicationServices
import CoreGraphics
import CoreImage
import CoreMedia
import CoreServices
import CoreVideo
import Foundation
import ImageIO
import ScreenCaptureKit
import VideoToolbox

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
    var trusted = AXIsProcessTrustedWithOptions([key: true] as CFDictionary)
    // Stay alive so the dialog isn't dropped while it's queued behind another TCC
    // prompt (that's why it used to appear only on a SECOND run), and pick up the
    // grant the moment the user flips it. Poll up to ~30s.
    var waited = 0
    while !trusted && waited < 30 {
        Thread.sleep(forTimeInterval: 1)
        trusted = AXIsProcessTrusted()
        waited += 1
    }
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

// Preflight subcommand: `reminal-capture ax-check` reports Accessibility
// (control-this-computer) status WITHOUT prompting — AXIsProcessTrusted (no
// options) never shows the dialog, unlike the `accessibility` subcommand above.
// The daemon runs this in its granted (sh.reminal) context so `reminal permissions`
// can poll for the grant one step at a time. Prints "ok"/"no".
if args.count >= 2, args[1] == "ax-check" {
    print(AXIsProcessTrusted() ? "ok" : "no")
    exit(0)
}

// Preflight subcommand: `reminal-capture auto-check` reports Automation (Apple
// Events → System Events) status WITHOUT prompting, via
// AEDeterminePermissionToAutomateTarget(askUserIfNeeded: false). noErr means the
// grant is already in place; anything else (denied, undetermined, target not
// running) counts as "not yet". The daemon runs this in its granted context for
// `reminal permissions` polling. Prints "ok"/"no".
if args.count >= 2, args[1] == "auto-check" {
    let bundleID = "com.apple.systemevents"
    var target = AEAddressDesc()
    let bytes = Array(bundleID.utf8)
    let created = bytes.withUnsafeBufferPointer { buf in
        AECreateDesc(typeApplicationBundleID, buf.baseAddress, buf.count, &target)
    }
    if created != noErr {
        print("no")
        exit(0)
    }
    let status = AEDeterminePermissionToAutomateTarget(&target, typeWildCard, typeWildCard, false)
    AEDisposeDesc(&target)
    print(status == noErr ? "ok" : "no")
    exit(0)
}

func die(_ msg: String) -> Never {
    FileHandle.standardError.write(Data((msg + "\n").utf8))
    exit(1)
}


// Parent lifeline + command channel: the agent holds our stdin pipe open; EOF
// means it's gone — including deaths that skip its cleanup (SIGKILL, crash, or
// its hot-restart exec, all of which close the fd). Without this, a helper on a
// STATIC window would outlive a dead agent forever: it only notices a closed
// stdout when a frame write fails, and a static window never writes. Armed only
// when stdin is a pipe, so running the helper by hand from a terminal still works.
// In h264 mode the same pipe doubles as a command channel: a "key\n" line forces
// an immediate IDR (see H264Encoder.requestKey). Unknown lines are ignored, so
// old agents that never write and future commands both stay compatible.
var h264Encoder: H264Encoder?
var stdinStat = stat()
if fstat(0, &stdinStat) == 0 && (stdinStat.st_mode & S_IFMT) == S_IFIFO {
    DispatchQueue.global(qos: .utility).async {
        var pending = Data()
        while true {
            // availableData, NOT read(upToCount:): the latter blocks until it
            // fills its whole buffer or hits EOF on a pipe, so a "key\n" line
            // would sit undelivered until the agent died (verified live — the
            // re-key commands all arrived in one burst at EOF, milliseconds
            // before exit). availableData returns as soon as any bytes arrive.
            let d = FileHandle.standardInput.availableData
            if d.isEmpty {
                exit(0) // EOF — parent is gone
            }
            pending.append(d)
            while let nl = pending.firstIndex(of: 0x0A) {
                let line = String(data: pending[pending.startIndex..<nl], encoding: .utf8)?
                    .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                pending = Data(pending[pending.index(after: nl)...])
                if line == "key" { h264Encoder?.requestKey() }
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
    FileHandle.standardError.write(Data("usage: reminal-capture <windowID|display:ID> [maxWidth] [quality] [fps] [jpeg|h264]\n".utf8))
    exit(2)
}
let maxWidth = args.count >= 3 ? (Int(args[2]) ?? 1100) : 1100
let quality = args.count >= 4 ? (Double(args[3]) ?? 45) / 100.0 : 0.45
// Ceiling is the panel's own rate (ProMotion tops out at 120); SCK never
// delivers faster than the display refreshes, so a higher request just idles.
let fps = args.count >= 5 ? max(1, min(120, Int(args[4]) ?? 60)) : 60
let useH264 = args.count >= 6 && args[5] == "h264"

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

// One queue for BOTH the SCStream sample handler and any out-of-band encoder
// submission (requestKey): VTCompressionSessionEncodeFrame is not thread-safe.
let sampleQueue = DispatchQueue(label: "reminal.capture")

// writeFramed emits one length-prefixed frame; flag is prepended inside the
// length when non-nil (h264 mode), absent for JPEG (legacy framing, byte-for-
// byte what pre-h264 agents parse). A closed pipe means the agent stopped the
// stream — exit quietly.
func writeFramed(_ payload: Data, flag: UInt8?) {
    var lenBE = UInt32(payload.count + (flag != nil ? 1 : 0)).bigEndian
    writeLock.lock()
    defer { writeLock.unlock() }
    do {
        try stdoutHandle.write(contentsOf: Data(bytes: &lenBE, count: 4))
        if let flag { try stdoutHandle.write(contentsOf: Data([flag])) }
        try stdoutHandle.write(contentsOf: payload)
    } catch {
        exit(0)
    }
}

// Frame flags for h264 framing (JPEG mode has no flag byte).
let flagH264Delta: UInt8 = 1
let flagH264Key: UInt8 = 2

// ---- H.264 encoder: VideoToolbox low-latency session → Annex-B access units ----
//
// Why H.264 instead of per-frame JPEG: temporal compression. Measured on a
// full-motion 1100×700 window: JPEG q45 at 30fps costs ~15 Mbps; H.264 at
// 2 Mbps is visually identical (SSIM 0.987) — a 7-10× wire reduction, which is
// the difference between "unusable on cellular P2P" and "trivial". The encoder
// runs on dedicated silicon, so CPU cost is at or below the JPEG path.
final class H264Encoder {
    private var session: VTCompressionSession
    private let lock = NSLock()
    private var forceNextKey = true // first frame must be an IDR regardless
    private var lastBuffer: CVPixelBuffer? // retained for static-window re-key

    init?(width: Int, height: Int, fps: Int) {
        // Low-latency rate control (hardware-only mode: no B-frames, no frame
        // delay, bitrate honored per-frame). If the machine's encoder doesn't
        // support it, fall back to a regular realtime session.
        var s: VTCompressionSession?
        let lowLatency = [kVTVideoEncoderSpecification_EnableLowLatencyRateControl: kCFBooleanTrue] as CFDictionary
        var status = VTCompressionSessionCreate(
            allocator: nil, width: Int32(width), height: Int32(height),
            codecType: kCMVideoCodecType_H264, encoderSpecification: lowLatency,
            imageBufferAttributes: nil, compressedDataAllocator: nil,
            outputCallback: nil, refcon: nil, compressionSessionOut: &s)
        if status != noErr || s == nil {
            status = VTCompressionSessionCreate(
                allocator: nil, width: Int32(width), height: Int32(height),
                codecType: kCMVideoCodecType_H264, encoderSpecification: nil,
                imageBufferAttributes: nil, compressedDataAllocator: nil,
                outputCallback: nil, refcon: nil, compressionSessionOut: &s)
        }
        guard status == noErr, let created = s else { return nil }
        session = created

        // ~0.08 bits per pixel per frame at 30fps lands at ~1.7 Mbps for
        // 1100×636 — measured visually transparent for UI content vs the JPEG
        // stream it replaces. Frame rate scales the budget SUBLINEARLY: at a
        // higher rate consecutive frames differ less, so each costs fewer bits.
        // (A linear term made 60fps ask for exactly 2× and the encoder happily
        // spent it — measured 3.5 vs 1.7 Mbps for a picture that needs ~1.5×.)
        // Clamped so tiny windows still get enough and huge desktops don't
        // flood a P2P link. Property failures are non-fatal: an encoder that
        // ignores a hint still produces decodable output.
        let fpsScale = pow(Double(fps) / 30.0, 0.6)
        let avgBitrate = max(600_000, min(6_000_000, Int(Double(width * height) * 30.0 * 0.08 * fpsScale)))
        VTSessionSetProperty(session, key: kVTCompressionPropertyKey_RealTime, value: kCFBooleanTrue)
        VTSessionSetProperty(session, key: kVTCompressionPropertyKey_ProfileLevel, value: kVTProfileLevel_H264_Main_AutoLevel)
        VTSessionSetProperty(session, key: kVTCompressionPropertyKey_AllowFrameReordering, value: kCFBooleanFalse)
        // Periodic IDR as a SAFETY NET, not the recovery mechanism: a viewer
        // that loses sync asks for a key immediately (see requestWindowKey), so
        // this only bounds how long corruption could persist if that request
        // itself went missing. 2s costs ~4% (one ~30 KB IDR per 120 deltas);
        // the 10s it replaced meant a lost AU could smear for ten seconds.
        VTSessionSetProperty(session, key: kVTCompressionPropertyKey_MaxKeyFrameIntervalDuration, value: 2 as CFNumber)
        VTSessionSetProperty(session, key: kVTCompressionPropertyKey_ExpectedFrameRate, value: fps as CFNumber)
        VTSessionSetProperty(session, key: kVTCompressionPropertyKey_AverageBitRate, value: avgBitrate as CFNumber)
        // Hard ceiling ~1.4× average over any 1s window so keyframe spikes stay
        // under the agent's per-message DataChannel budget.
        VTSessionSetProperty(session, key: kVTCompressionPropertyKey_DataRateLimits,
                             value: [avgBitrate / 8 * 14 / 10, 1] as CFArray)
        VTCompressionSessionPrepareToEncodeFrames(session)
    }

    // encode compresses one captured frame; called on the sample handler queue.
    func encode(_ pixelBuffer: CVPixelBuffer) {
        lock.lock()
        let key = forceNextKey
        forceNextKey = false
        lastBuffer = pixelBuffer
        lock.unlock()
        submit(pixelBuffer, forceKey: key)
    }

    // requestKey forces the next frame to be an IDR. For a static window no
    // next frame is coming, so the cached last frame is re-encoded immediately
    // — a newly attached viewer must not wait for on-screen change to see
    // anything. Called from the stdin command thread.
    //
    // Two hard-won constraints (both verified live against SCK capture):
    // 1. The re-encode MUST run on the same queue as live frame submission —
    //    VTCompressionSessionEncodeFrame is not thread-safe, and a submission
    //    racing the sample handler's is silently swallowed (returns noErr, the
    //    output handler simply never fires). All submits go via sampleQueue.
    // 2. The cached buffer is DEEP-COPIED first: SCK's pooled IOSurface buffers
    //    misbehave on re-submission. A plain BGRA blit into a fresh buffer only
    //    costs on re-key, never per frame.
    func requestKey() {
        lock.lock()
        forceNextKey = true
        let cached = lastBuffer
        lock.unlock()
        guard let cached else { return } // no frame yet: the first one is a key anyway
        sampleQueue.async {
            guard let copy = Self.copyPixelBuffer(cached) else { return } // next live frame keys instead
            self.lock.lock()
            self.forceNextKey = false
            self.lock.unlock()
            self.submit(copy, forceKey: true)
        }
    }

    private static func copyPixelBuffer(_ src: CVPixelBuffer) -> CVPixelBuffer? {
        let w = CVPixelBufferGetWidth(src), h = CVPixelBufferGetHeight(src)
        var dstOpt: CVPixelBuffer?
        // IOSurface-backed like the live SCK frames: the hardware encoder
        // silently drops a malloc-backed buffer slipped into an IOSurface
        // stream (no callback at all).
        let attrs = [kCVPixelBufferIOSurfacePropertiesKey: [:] as CFDictionary] as CFDictionary
        guard CVPixelBufferCreate(nil, w, h, CVPixelBufferGetPixelFormatType(src), attrs, &dstOpt) == kCVReturnSuccess,
              let dst = dstOpt else { return nil }
        CVPixelBufferLockBaseAddress(src, .readOnly)
        CVPixelBufferLockBaseAddress(dst, [])
        defer {
            CVPixelBufferUnlockBaseAddress(dst, [])
            CVPixelBufferUnlockBaseAddress(src, .readOnly)
        }
        guard let srcBase = CVPixelBufferGetBaseAddress(src),
              let dstBase = CVPixelBufferGetBaseAddress(dst) else { return nil }
        let srcStride = CVPixelBufferGetBytesPerRow(src)
        let dstStride = CVPixelBufferGetBytesPerRow(dst)
        let rowBytes = min(srcStride, dstStride)
        for y in 0..<h {
            memcpy(dstBase + y * dstStride, srcBase + y * srcStride, rowBytes)
        }
        return dst
    }

    private func submit(_ pixelBuffer: CVPixelBuffer, forceKey: Bool) {
        // Host-clock PTS: always monotonic, even when requestKey re-submits the
        // cached buffer out of band (SCK timestamps would repeat there).
        let pts = CMClockGetTime(CMClockGetHostTimeClock())
        let props = forceKey ? [kVTEncodeFrameOptionKey_ForceKeyFrame: kCFBooleanTrue] as CFDictionary : nil
        let status = VTCompressionSessionEncodeFrame(
            session, imageBuffer: pixelBuffer, presentationTimeStamp: pts,
            duration: .invalid, frameProperties: props, infoFlagsOut: nil
        ) { status, _, sampleBuffer in
            guard status == noErr, let sampleBuffer else { return }
            self.emit(sampleBuffer)
        }
        if status != noErr {
            die("h264 encode failed: \(status)") // agent restarts / falls back to jpeg
        }
    }

    // emit converts one compressed sample (AVCC: length-prefixed NALs, parameter
    // sets off in the format description) to a self-contained Annex-B access
    // unit and writes it as a framed message. Keyframes carry SPS/PPS inline so
    // any AU tagged "key" is a valid decoder entry point on its own.
    private func emit(_ sampleBuffer: CMSampleBuffer) {
        let attachments = CMSampleBufferGetSampleAttachmentsArray(sampleBuffer, createIfNecessary: false)
            as? [[CFString: Any]]
        let notSync = attachments?.first?[kCMSampleAttachmentKey_NotSync] as? Bool ?? false
        let isKey = !notSync

        let startCode = Data([0, 0, 0, 1])
        var out = Data()
        var nalHeaderLen: Int32 = 4
        if let fd = CMSampleBufferGetFormatDescription(sampleBuffer) {
            var psCount = 0
            CMVideoFormatDescriptionGetH264ParameterSetAtIndex(
                fd, parameterSetIndex: 0, parameterSetPointerOut: nil,
                parameterSetSizeOut: nil, parameterSetCountOut: &psCount,
                nalUnitHeaderLengthOut: &nalHeaderLen)
            if isKey {
                for i in 0..<psCount {
                    var ptr: UnsafePointer<UInt8>?
                    var size = 0
                    let st = CMVideoFormatDescriptionGetH264ParameterSetAtIndex(
                        fd, parameterSetIndex: i, parameterSetPointerOut: &ptr,
                        parameterSetSizeOut: &size, parameterSetCountOut: nil,
                        nalUnitHeaderLengthOut: nil)
                    if st == noErr, let ptr {
                        out.append(startCode)
                        out.append(ptr, count: size)
                    }
                }
            }
        }

        guard let block = CMSampleBufferGetDataBuffer(sampleBuffer) else { return }
        var totalLen = 0
        var dataPtr: UnsafeMutablePointer<CChar>?
        guard CMBlockBufferGetDataPointer(block, atOffset: 0, lengthAtOffsetOut: nil,
                                          totalLengthOut: &totalLen, dataPointerOut: &dataPtr) == kCMBlockBufferNoErr,
              let base = dataPtr else { return }
        let hdr = Int(nalHeaderLen)
        var off = 0
        base.withMemoryRebound(to: UInt8.self, capacity: totalLen) { bytes in
            while off + hdr <= totalLen {
                var nalLen = 0
                for i in 0..<hdr { nalLen = nalLen << 8 | Int(bytes[off + i]) }
                guard nalLen > 0, off + hdr + nalLen <= totalLen else { break }
                out.append(startCode)
                out.append(UnsafeBufferPointer(start: bytes + off + hdr, count: nalLen))
                off += hdr + nalLen
            }
        }
        guard !out.isEmpty else { return }
        writeFramed(out, flag: isKey ? flagH264Key : flagH264Delta)
    }
}



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

        // h264 mode: hand the BGRA buffer straight to VideoToolbox — no CGImage,
        // no JPEG. The encoder emits framed Annex-B AUs itself.
        if let enc = h264Encoder {
            enc.encode(pixelBuffer)
            return
        }

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
        writeFramed(jpeg, flag: nil)
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

if useH264 {
    guard let enc = H264Encoder(width: config.width, height: config.height, fps: fps) else {
        // No usable H.264 encoder on this machine: exit non-zero so the agent
        // falls back to requesting a JPEG stream instead.
        die("h264: VTCompressionSession unavailable")
    }
    h264Encoder = enc
}

let output = FrameOutput()
let stream = SCStream(filter: filter, configuration: config, delegate: output)
do {
    try stream.addStreamOutput(output, type: .screen, sampleHandlerQueue: sampleQueue)
} catch {
    die("addStreamOutput: \(error.localizedDescription)")
}
stream.startCapture { error in
    if let error { die("startCapture: \(error.localizedDescription)") }
}

// SCStream delivers frames on its own queue; keep the process alive until the
// agent kills it (stream stop) or the pipe closes.
CFRunLoopRun()
