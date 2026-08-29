// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar
//
// reminal-overlay — PROTOTYPE. A floating annotation badge that glues itself to
// another application's window, so an agent can leave a note on the thing the
// note is about instead of burying it in a terminal the user isn't looking at.
//
// Why a separate helper (same reasoning as reminal-capture): the Go agent builds
// with CGO_ENABLED=0 and can't call AppKit. This is a small native process the
// daemon drives over a line protocol.
//
// How the floating works, which is the part worth prototyping:
//   * The badge lives in an NSPanel with styleMask [.borderless,
//     .nonactivatingPanel] at .floating level. Non-activating is the load-bearing
//     flag: clicking "Done" must NOT steal focus from the app the user is working
//     in, and a normal NSWindow would.
//   * collectionBehavior [.canJoinAllSpaces, .fullScreenAuxiliary] keeps it
//     visible when the user switches Spaces or the target goes fullscreen.
//   * Position is polled from CGWindowListCopyWindowInfo (bounds + on-screen) and
//     converted CG->NS coordinates. Polling (not AXObserver) on purpose for the
//     prototype: window *bounds* need no Accessibility grant, so this runs with
//     zero TCC prompts. The refinement is an AXObserver on kAXMoved/kAXResized —
//     event-driven, no poll — but that needs the AX grant reminal already asks
//     for, so it is a later optimization, not a blocker.
//   * The overlay is a separate window, so ScreenCaptureKit capture of the TARGET
//     window does not include it. The phone viewer has to draw its own copy of
//     the same comment data (it is HTML, so that is easy) — the badge is a
//     desktop affordance, not something that rides the video stream.
//
// Protocol (newline-delimited JSON, stdin commands / stdout events) so the
// daemon can drive it and MCP tools map 1:1 onto it:
//   in : {"cmd":"attach","window":1234}
//        {"cmd":"upsert","id":"c1","status":"attention","title":"...","body":"...","author":"claude"}
//        {"cmd":"remove","id":"c1"} | {"cmd":"clear"} | {"cmd":"quit"}
//   out: {"event":"handback","id":"c1","window":1234}   <- user pressed Done
//        {"event":"dismiss","id":"c1","window":1234}
//        {"event":"expand","window":1234}
//        {"event":"closed","window":1234}               <- window gone, then exit
//
// Lifetime: comments live exactly as long as their window. Nothing is persisted —
// a closed window's pending items are forgotten on purpose, which is why the
// ephemeral CGWindowID is a perfectly good key and no durable identity is needed.
// A minimised or hidden window is not a closed one, and keeps its list.
//
// Subcommands: `windows` lists candidate window ids; `demo [id]` attaches sample
// comments to a window (default: the frontmost one) so the interaction can be
// eyeballed without wiring up the daemon first.

import AppKit
import CoreGraphics
import Foundation

// ---------------------------------------------------------------- model

/// Status drives the dot colour. Red is the only one that pulses — attention is
/// the whole point of the badge, everything else is ambient state.
enum Status: String {
    case attention  // red    — blocked on the user
    case working    // amber  — agent is mid-task
    case info       // blue   — FYI, no action
    case done       // green  — finished, nothing owed
    case handback   // purple — user pressed Done, agent's turn again

    var color: NSColor {
        switch self {
        case .attention: return NSColor(srgbRed: 1.00, green: 0.27, blue: 0.23, alpha: 1)
        case .working:   return NSColor(srgbRed: 1.00, green: 0.74, blue: 0.13, alpha: 1)
        case .info:      return NSColor(srgbRed: 0.16, green: 0.55, blue: 1.00, alpha: 1)
        case .done:      return NSColor(srgbRed: 0.20, green: 0.82, blue: 0.35, alpha: 1)
        case .handback:  return NSColor(srgbRed: 0.75, green: 0.38, blue: 0.95, alpha: 1)
        }
    }

    var label: String {
        switch self {
        case .attention: return "Needs you"
        case .working:   return "Working"
        case .info:      return "Note"
        case .done:      return "Done"
        case .handback:  return "Back to agent"
        }
    }

    /// Only a status the user must act on earns motion.
    var pulses: Bool { self == .attention }
}

struct Comment {
    var id: String
    var status: Status
    var title: String
    var body: String
    var author: String
    var created: Date
}

/// "2m", "just now" — the badge is glanceable, so the timestamp is too.
func shortAge(_ d: Date) -> String {
    let s = Int(Date().timeIntervalSince(d))
    if s < 45 { return "just now" }
    if s < 3600 { return "\(max(1, s / 60))m ago" }
    if s < 86400 { return "\(s / 3600)h ago" }
    return "\(s / 86400)d ago"
}

// ---------------------------------------------------------------- window tracking

struct WinInfo {
    let bounds: CGRect   // CG coords: origin top-left of the primary display
    let onscreen: Bool
    let owner: String
    let title: String
}

func windowInfo(_ wid: CGWindowID) -> WinInfo? {
    guard let arr = CGWindowListCopyWindowInfo([.optionIncludingWindow], wid) as? [[String: Any]],
          let d = arr.first,
          let bdict = d[kCGWindowBounds as String] as? NSDictionary,
          let rect = CGRect(dictionaryRepresentation: bdict) else { return nil }
    return WinInfo(bounds: rect,
                   onscreen: (d[kCGWindowIsOnscreen as String] as? Bool) ?? false,
                   owner: d[kCGWindowOwnerName as String] as? String ?? "",
                   title: d[kCGWindowName as String] as? String ?? "")
}

/// On-screen, normal-layer, reasonably sized windows, front to back — the things
/// a user would actually recognise as "a window".
func candidateWindows() -> [(id: CGWindowID, info: WinInfo)] {
    guard let arr = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements],
                                               kCGNullWindowID) as? [[String: Any]] else { return [] }
    var out: [(CGWindowID, WinInfo)] = []
    let mypid = ProcessInfo.processInfo.processIdentifier
    for d in arr {
        guard let layer = d[kCGWindowLayer as String] as? Int, layer == 0,
              let wid = d[kCGWindowNumber as String] as? CGWindowID,
              let pid = d[kCGWindowOwnerPID as String] as? Int32, pid != mypid,
              let bdict = d[kCGWindowBounds as String] as? NSDictionary,
              let rect = CGRect(dictionaryRepresentation: bdict),
              rect.width > 120, rect.height > 80 else { continue }
        out.append((wid, WinInfo(bounds: rect, onscreen: true,
                                 owner: d[kCGWindowOwnerName as String] as? String ?? "",
                                 title: d[kCGWindowName as String] as? String ?? "")))
    }
    return out
}

/// CG global coords put (0,0) at the top-left of the primary display and grow
/// downward; AppKit puts it at the bottom-left and grows upward. Flip around the
/// primary screen — the one whose NS frame origin is (0,0).
func primaryScreen() -> NSScreen? {
    NSScreen.screens.first(where: { $0.frame.origin == .zero }) ?? NSScreen.main ?? NSScreen.screens.first
}

func cgToNS(_ r: CGRect) -> CGRect {
    guard let p = primaryScreen() else { return r }
    return CGRect(x: r.origin.x, y: p.frame.maxY - r.origin.y - r.height,
                  width: r.width, height: r.height)
}

func nsToCG(_ r: CGRect) -> CGRect {
    guard let p = primaryScreen() else { return r }
    return CGRect(x: r.origin.x, y: p.frame.maxY - r.maxY, width: r.width, height: r.height)
}

/// One on-screen window enumeration shared by every badge in the process.
///
/// This is the point of multiplexing. Memory was the obvious saving — a helper
/// per window cost ~26MB each — but the enumeration was the quiet one: every
/// badge independently walked the whole window list for its occlusion and
/// Mission Control checks, so twelve badges meant twelve identical walks per
/// tick. Cached for a frame, it is one walk regardless of how many are up.
enum WindowList {
    private static var cached: [[String: Any]] = []
    private static var stamp: CFTimeInterval = -1

    static func onScreen() -> [[String: Any]] {
        let now = CACurrentMediaTime()
        if now - stamp > 0.03 {
            cached = (CGWindowListCopyWindowInfo([.optionOnScreenOnly],
                                                 kCGNullWindowID) as? [[String: Any]]) ?? []
            stamp = now
        }
        return cached
    }
}

// ---------------------------------------------------------------- drawing helpers

let cardWidth: CGFloat = 312
let pillHeight: CGFloat = 20     // deliberately small: at rest this is furniture
let inset: CGFloat = 10          // gap between the badge and the window corner

/// Resting opacity. The badge should read as a faint marker you can ignore until
/// you look at it; hovering (or expanding) brings it to full strength.
let restAlpha: CGFloat = 0.72

let debugLayout = ProcessInfo.processInfo.environment["REMINAL_OVERLAY_DEBUG"] != nil

/// Which corner of the target window the badge hugs. Which one is least
/// intrusive is per-app — top-right lands in Chrome's tab strip but is empty
/// title-bar space in an editor or terminal — so it is selectable.
enum Corner: String {
    case tr, br, tl, bl
    var right: Bool { self == .tr || self == .br }
    var top: Bool { self == .tr || self == .tl }
}

/// How the badge relates to the window it annotates.
///   inside — sits on the window's content, hugging the chosen corner
///   notch  — an inverted MacBook notch growing out of the top edge
///   float  — a detached capsule hovering just off the edge (the elegant one)
enum Placement: String { case inside, notch, float }

// Notch geometry. The badge protrudes up out of the window's top edge and flares
// back into it with concave fillets — a MacBook notch turned inside out.
let notchHeight: CGFloat = 26
let notchTopRadius: CGFloat = 8
let notchFillet: CGFloat = 8
let notchCornerInset: CGFloat = 20   // clear of the window's own rounded corner
let notchTuck: CGFloat = 5           // sink into the frame so no seam shows

/// The silhouette, in panel coordinates with y=0 sitting on the window's top edge:
/// concave flare up out of the edge, straight sides, rounded top, flare back down.
func notchPath(_ size: NSSize) -> NSBezierPath {
    let w = size.width, h = size.height
    let rf = min(notchFillet, w / 2), rt = min(notchTopRadius, (w - 2 * rf) / 2)
    let p = NSBezierPath()
    p.move(to: NSPoint(x: 0, y: 0))
    // Concave: centre sits outside the shape, so the edge curves away from the
    // tab body and melts into the window edge instead of meeting it at a corner.
    p.appendArc(withCenter: NSPoint(x: 0, y: rf), radius: rf,
                startAngle: -90, endAngle: 0, clockwise: false)
    p.line(to: NSPoint(x: rf, y: h - rt))
    p.appendArc(withCenter: NSPoint(x: rf + rt, y: h - rt), radius: rt,
                startAngle: 180, endAngle: 90, clockwise: true)
    p.line(to: NSPoint(x: w - rf - rt, y: h))
    p.appendArc(withCenter: NSPoint(x: w - rf - rt, y: h - rt), radius: rt,
                startAngle: 90, endAngle: 0, clockwise: true)
    p.line(to: NSPoint(x: w - rf, y: rf))
    p.appendArc(withCenter: NSPoint(x: w, y: rf), radius: rf,
                startAngle: 180, endAngle: 270, clockwise: false)
    p.close()
    return p
}

/// NSVisualEffectView can only be shaped by a mask image — a layer cornerRadius
/// cannot express concave corners.
func notchMask(_ size: NSSize) -> NSImage {
    let img = NSImage(size: size)
    img.lockFocus()
    NSColor.black.setFill()
    notchPath(size).fill()
    img.unlockFocus()
    return img
}

/// Transparent margin kept inside the panel in float mode so the shadow can be
/// drawn rather than left to the window server — a drawn shadow can be soft and
/// diffuse, where the system window shadow is heavy and clips to the frame.
let floatPad: CGFloat = 14
let floatGap: CGFloat = 9         // clear air between window edge and pill

/// The contrast floor under the material, plus the glass highlight. It has to be
/// a drawn path, not a plain background colour: `maskImage` shapes the material
/// only, so a rectangular subview inside the effect view punches the silhouette
/// straight back out.
final class ScrimView: NSView {
    var shape: NSBezierPath?
    /// A faint light wash down from the top edge. This is most of what separates
    /// "grey rounded rectangle" from something that reads as glass.
    var glass = false
    override func draw(_ dirtyRect: NSRect) {
        let p = shape ?? NSBezierPath(rect: bounds)
        NSColor(white: 0, alpha: 0.30).setFill()
        p.fill()
        guard glass else { return }
        NSGraphicsContext.saveGraphicsState()
        p.setClip()
        NSGradient(starting: NSColor(white: 1, alpha: 0.16),
                   ending: NSColor(white: 1, alpha: 0.0))?
            .draw(in: NSRect(x: 0, y: bounds.midY, width: bounds.width, height: bounds.height / 2),
                  angle: -90)
        NSGraphicsContext.restoreGraphicsState()
    }
    override func hitTest(_ point: NSPoint) -> NSView? { nil }
}

/// Draws only the shadow *spill* around a shape: clip to everything outside the
/// path, then fill the path opaque so nothing but its shadow survives the clip.
/// Gives a soft diffuse drop that the system window shadow can't match.
final class SoftShadowView: NSView {
    var shape: NSBezierPath?
    override func draw(_ dirtyRect: NSRect) {
        guard let p = shape else { return }
        NSGraphicsContext.saveGraphicsState()
        let clip = NSBezierPath(rect: bounds)
        clip.append(p)
        clip.windingRule = .evenOdd
        clip.setClip()
        let sh = NSShadow()
        sh.shadowBlurRadius = 13
        sh.shadowOffset = NSSize(width: 0, height: -3)
        sh.shadowColor = NSColor(white: 0, alpha: 0.36)
        sh.set()
        NSColor.black.setFill()
        p.fill()
        NSGraphicsContext.restoreGraphicsState()
    }
    override func hitTest(_ point: NSPoint) -> NSView? { nil }
}

/// Hairline along the silhouette. Separate view because a custom path can't use
/// the layer's border. Never takes clicks.
final class ShapeStrokeView: NSView {
    var path: NSBezierPath?
    override func draw(_ dirtyRect: NSRect) {
        guard let p = path else { return }
        NSColor(white: 1, alpha: 0.16).setStroke()
        p.lineWidth = 1
        p.stroke()
    }
    override func hitTest(_ point: NSPoint) -> NSView? { nil }
}

func attr(_ s: String, _ font: NSFont, _ color: NSColor) -> NSAttributedString {
    let para = NSMutableParagraphStyle()
    para.lineBreakMode = .byWordWrapping
    para.lineSpacing = 1.5
    return NSAttributedString(string: s, attributes: [.font: font, .foregroundColor: color,
                                                      .paragraphStyle: para])
}

func textHeight(_ a: NSAttributedString, width: CGFloat) -> CGFloat {
    ceil(a.boundingRect(with: NSSize(width: width, height: 10_000),
                        options: [.usesLineFragmentOrigin, .usesFontLeading]).height)
}

func drawDot(_ center: CGPoint, radius: CGFloat, color: NSColor, pulsePhase: CGFloat?) {
    // A pulsing halo, drawn behind, for statuses that want the eye. Kept faint —
    // a badge that throbs hard is the thing people turn off.
    if let phase = pulsePhase {
        let grow = radius + 3.5 * phase
        color.withAlphaComponent(0.26 * (1 - phase)).setFill()
        NSBezierPath(ovalIn: CGRect(x: center.x - grow, y: center.y - grow,
                                    width: grow * 2, height: grow * 2)).fill()
    }
    let rect = CGRect(x: center.x - radius, y: center.y - radius,
                      width: radius * 2, height: radius * 2)
    let disc = NSBezierPath(ovalIn: rect)
    // Vertical gradient rather than a flat fill: a lit top edge is what makes a
    // 7pt dot look like an object instead of a printed circle.
    let top = color.blended(withFraction: 0.30, of: .white) ?? color
    let bottom = color.blended(withFraction: 0.12, of: .black) ?? color
    NSGradient(starting: top, ending: bottom)?.draw(in: disc, angle: -90)
    // Hairline rim keeps the dot crisp against a bright window behind the blur.
    NSColor(white: 0, alpha: 0.18).setStroke()
    disc.lineWidth = 0.5
    disc.stroke()
}

// ---------------------------------------------------------------- small button

final class PillButton: NSView {
    private let title: String
    private let accent: NSColor
    private let filled: Bool
    private let ink: NSColor
    private let action: () -> Void
    private var hovering = false

    init(title: String, filled: Bool, accent: NSColor,
         ink: NSColor = .white, action: @escaping () -> Void) {
        self.title = title; self.filled = filled; self.accent = accent
        self.ink = ink; self.action = action
        let font = NSFont.systemFont(ofSize: 11.5, weight: .medium)
        let w = ceil(attr(title, font, .white).size().width) + 22
        super.init(frame: NSRect(x: 0, y: 0, width: w, height: 22))
    }
    required init?(coder: NSCoder) { fatalError() }

    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        trackingAreas.forEach(removeTrackingArea)
        addTrackingArea(NSTrackingArea(rect: bounds,
                                       options: [.mouseEnteredAndExited, .activeAlways],
                                       owner: self, userInfo: nil))
    }
    override func mouseEntered(with event: NSEvent) { hovering = true; needsDisplay = true }
    override func mouseExited(with event: NSEvent) { hovering = false; needsDisplay = true }
    override func mouseUp(with event: NSEvent) {
        if bounds.contains(convert(event.locationInWindow, from: nil)) { action() }
    }
    override func resetCursorRects() { addCursorRect(bounds, cursor: .pointingHand) }

    override func draw(_ dirtyRect: NSRect) {
        let path = NSBezierPath(roundedRect: bounds, xRadius: bounds.height / 2,
                                yRadius: bounds.height / 2)
        if filled {
            accent.withAlphaComponent(hovering ? 1.0 : 0.88).setFill(); path.fill()
        } else {
            NSColor(white: 1, alpha: hovering ? 0.14 : 0.07).setFill(); path.fill()
            NSColor(white: 1, alpha: 0.18).setStroke(); path.lineWidth = 1; path.stroke()
        }
        let color: NSColor = filled ? ink : NSColor(white: 1, alpha: 0.75)
        let a = attr(title, NSFont.systemFont(ofSize: 11.5, weight: .medium), color)
        let size = a.size()
        a.draw(at: NSPoint(x: (bounds.width - size.width) / 2,
                           y: (bounds.height - size.height) / 2 + 0.5))
    }
}

// ---------------------------------------------------------------- collapsed pill

/// The resting state: a dot per comment. This is what sits on the window 99% of
/// the time, so it stays tiny and readable at a glance from across the desk.
final class PillView: NSView {
    var comments: [Comment] = [] { didSet { needsDisplay = true } }
    var onClick: () -> Void = {}
    private var pulseTimer: Timer?

    static func width(for comments: [Comment]) -> CGFloat {
        let n = min(comments.count, 4)
        let dots = CGFloat(n) * 7 + CGFloat(max(0, n - 1)) * 5
        let extra: CGFloat = comments.count > 4 ? 18 : 0
        return 9 + dots + extra + 9
    }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        pulseTimer?.invalidate()
        guard window != nil else { return }
        // 15fps is plenty for a breathing halo on a ~60pt view. Skipped entirely
        // when nothing is pulsing, when the badge is hidden behind another window,
        // or while it is fading out mid-expand — none of which are visible.
        let t = Timer(timeInterval: 1.0 / 15.0, repeats: true) { [weak self] _ in
            guard let self = self,
                  self.window?.isVisible == true,
                  !self.isHidden, self.alphaValue > 0.01,
                  self.comments.contains(where: { $0.status.pulses }) else { return }
            self.needsDisplay = true
        }
        RunLoop.main.add(t, forMode: .common)
        pulseTimer = t
    }

    override func draw(_ dirtyRect: NSRect) {
        let phase = CGFloat((sin(CACurrentMediaTime() * 1.9) + 1) / 2)  // 0..1 breathe
        var x: CGFloat = 9 + 3.5
        for c in comments.prefix(4) {
            drawDot(CGPoint(x: x, y: bounds.midY), radius: 3.5, color: c.status.color,
                    pulsePhase: c.status.pulses ? phase : nil)
            x += 12
        }
        if comments.count > 4 {
            let a = attr("+\(comments.count - 4)", NSFont.systemFont(ofSize: 10, weight: .semibold),
                         NSColor(white: 1, alpha: 0.7))
            a.draw(at: NSPoint(x: x - 4, y: bounds.midY - a.size().height / 2))
        }
    }

    override func mouseUp(with event: NSEvent) { onClick() }
    override func resetCursorRects() { addCursorRect(bounds, cursor: .pointingHand) }
}

// ---------------------------------------------------------------- expanded row

final class CommentRow: NSView {
    let comment: Comment
    var onHandback: (String) -> Void = { _ in }
    var onDismiss: (String) -> Void = { _ in }

    private static let titleFont = NSFont.systemFont(ofSize: 12.5, weight: .semibold)
    private static let bodyFont  = NSFont.systemFont(ofSize: 12, weight: .regular)
    private static let metaFont  = NSFont.systemFont(ofSize: 10.5, weight: .regular)
    private static let textLeft: CGFloat = 32
    private static var textWidth: CGFloat { cardWidth - textLeft - 14 }

    /// Hand-back is offered only where it means something — "Working" is the
    /// agent's turn, so a Done there would be nonsense. Dismiss, by contrast, is
    /// ALWAYS available: every row must be removable, or a stuck "Working" note
    /// from a crashed agent can never be cleared.
    static func buttons(for s: Status) -> (handback: String?, dismiss: String) {
        switch s {
        case .attention: return ("Done", "Dismiss")
        case .info:      return (nil, "Got it")
        case .working:   return (nil, "Dismiss")
        case .done, .handback: return (nil, "Clear")
        }
    }

    static func height(for c: Comment) -> CGFloat {
        let t = textHeight(attr(c.title, titleFont, .white), width: textWidth)
        let b = c.body.isEmpty ? 0 : textHeight(attr(c.body, bodyFont, .white), width: textWidth) + 4
        return 12 + t + b + 5 + 14 + 30 + 12   // pad title body gap meta actions pad
    }

    private let showSeparator: Bool

    init(comment: Comment, showSeparator: Bool = false) {
        self.comment = comment
        self.showSeparator = showSeparator
        super.init(frame: NSRect(x: 0, y: 0, width: cardWidth, height: CommentRow.height(for: comment)))
        let btns = CommentRow.buttons(for: comment.status)
        var x = CommentRow.textLeft
        if let label = btns.handback {
            // Neutral primary, not the status colour: a red "Done" reads as
            // destructive when it is the opposite — it hands work back.
            let done = PillButton(title: label, filled: true,
                                  accent: NSColor(white: 1, alpha: 0.92),
                                  ink: NSColor(white: 0.08, alpha: 1)) { [weak self] in
                guard let self = self else { return }
                self.onHandback(self.comment.id)
            }
            done.frame.origin = NSPoint(x: x, y: 12)
            addSubview(done)
            x += done.frame.width + 6
        }
        let dismiss = PillButton(title: btns.dismiss, filled: false, accent: .white) { [weak self] in
            guard let self = self else { return }
            self.onDismiss(self.comment.id)
        }
        dismiss.frame.origin = NSPoint(x: x, y: 12)
        addSubview(dismiss)
    }
    required init?(coder: NSCoder) { fatalError() }

    override func draw(_ dirtyRect: NSRect) {
        let s = comment.status
        if showSeparator {
            NSColor(white: 1, alpha: 0.10).setFill()
            NSRect(x: 14, y: bounds.height - 1, width: bounds.width - 28, height: 1).fill()
        }
        var y = bounds.height - 12

        drawDot(CGPoint(x: 18, y: y - 7), radius: 4.5, color: s.color, pulsePhase: nil)

        let title = attr(comment.title, CommentRow.titleFont, .white)
        let th = textHeight(title, width: CommentRow.textWidth)
        title.draw(with: NSRect(x: CommentRow.textLeft, y: y - th,
                                width: CommentRow.textWidth, height: th),
                   options: [.usesLineFragmentOrigin, .usesFontLeading])
        y -= th

        if !comment.body.isEmpty {
            let body = attr(comment.body, CommentRow.bodyFont, NSColor(white: 1, alpha: 0.62))
            let bh = textHeight(body, width: CommentRow.textWidth)
            body.draw(with: NSRect(x: CommentRow.textLeft, y: y - bh - 4,
                                   width: CommentRow.textWidth, height: bh),
                      options: [.usesLineFragmentOrigin, .usesFontLeading])
            y -= bh + 4
        }

        // Meta gets its own line under the body — right-aligning it onto the
        // button row collided with "Dismiss" at realistic string lengths.
        let meta = attr("\(s.label) · \(comment.author) · \(shortAge(comment.created))",
                        CommentRow.metaFont, NSColor(white: 1, alpha: 0.45))
        meta.draw(at: NSPoint(x: CommentRow.textLeft, y: y - 19))
    }
}

// ---------------------------------------------------------------- expanded card

/// Rows stack downward from the top, which is what a scroll view expects of its
/// document view — otherwise the list starts scrolled to the oldest entry.
final class FlippedStack: NSView {
    override var isFlipped: Bool { true }
}

/// How many rows are shown before the list starts scrolling. The card is a
/// glance surface: past three it stops being readable at a look.
let maxVisibleRows = 3

/// Hard ceiling on a window's list. Three are visible and the rest scroll, so
/// this is not about legibility — it is a backstop against an agent in a loop.
let maxComments = 50

final class CardView: NSView {
    static let headerHeight: CGFloat = 34

    /// Natural height: header plus at most `maxVisibleRows` rows.
    static func height(for comments: [Comment]) -> CGFloat {
        headerHeight + comments.prefix(maxVisibleRows)
            .reduce(CGFloat(0)) { $0 + CommentRow.height(for: $1) }
    }

    init(comments: [Comment], target: String,
         onHandback: @escaping (String) -> Void,
         onDismiss: @escaping (String) -> Void,
         onDismissAll: @escaping () -> Void,
         onCollapse: @escaping () -> Void) {
        let header = CardView.headerHeight
        let visibleH = CardView.height(for: comments) - header
        let totalH = comments.reduce(CGFloat(0)) { $0 + CommentRow.height(for: $1) }
        super.init(frame: NSRect(x: 0, y: 0, width: cardWidth, height: header + visibleH))
        self.target = target

        let stack = FlippedStack(frame: NSRect(x: 0, y: 0, width: cardWidth, height: totalH))
        var y: CGFloat = 0
        for (i, c) in comments.enumerated() {
            let row = CommentRow(comment: c, showSeparator: i > 0)
            row.onHandback = onHandback
            row.onDismiss = onDismiss
            row.frame.origin = NSPoint(x: 0, y: y)
            y += row.frame.height
            stack.addSubview(row)
        }

        let scroll = NSScrollView(frame: NSRect(x: 0, y: 0, width: cardWidth, height: visibleH))
        scroll.drawsBackground = false
        scroll.hasVerticalScroller = comments.count > maxVisibleRows
        scroll.autohidesScrollers = true
        scroll.scrollerStyle = .overlay
        scroll.scrollerKnobStyle = .light      // the card is dark
        scroll.verticalScrollElasticity = .allowed
        scroll.documentView = stack
        addSubview(scroll)
        // Start at the newest entry rather than wherever AppKit lands.
        scroll.contentView.scroll(to: .zero)

        let collapse = PillButton(title: "Hide", filled: false, accent: .white, action: onCollapse)
        collapse.frame.origin = NSPoint(x: cardWidth - collapse.frame.width - 12,
                                        y: frame.height - header + 6)
        addSubview(collapse)

        var leftmostButton = collapse.frame.minX
        if comments.count > 1 {
            let all = PillButton(title: "Dismiss all", filled: false, accent: .white,
                                 action: onDismissAll)
            all.frame.origin = NSPoint(x: collapse.frame.minX - all.frame.width - 6,
                                       y: frame.height - header + 6)
            addSubview(all)
            leftmostButton = all.frame.minX
        }
        // Window titles are arbitrarily long; without this the header runs
        // straight under the buttons.
        titleWidth = max(40, leftmostButton - 14 - 8)
    }
    required init?(coder: NSCoder) { fatalError() }
    private var target: String = ""
    private var titleWidth: CGFloat = 200

    override func draw(_ dirtyRect: NSRect) {
        // Header: which window these notes belong to, clipped to the space the
        // buttons leave and tail-truncated.
        let para = NSMutableParagraphStyle()
        para.lineBreakMode = .byTruncatingTail
        let a = NSAttributedString(
            string: "reminal · \(target)",
            attributes: [.font: NSFont.systemFont(ofSize: 11.5, weight: .semibold),
                         .foregroundColor: NSColor(white: 1, alpha: 0.55),
                         .paragraphStyle: para])
        a.draw(with: NSRect(x: 14, y: bounds.height - 23, width: titleWidth, height: 16),
               options: [.usesLineFragmentOrigin])
        NSColor(white: 1, alpha: 0.10).setFill()
        NSRect(x: 0, y: bounds.height - CardView.headerHeight,
               width: bounds.width, height: 1).fill()
    }
}

// ---------------------------------------------------------------- panel

final class OverlayPanel: NSPanel {
    override var canBecomeKey: Bool { false }
    override var canBecomeMain: Bool { false }
}

/// Transparent container that owns hover tracking. It sits above the blurred
/// background and below the content, so the chrome can fade independently of the
/// dots — at rest the badge should read as a few floating dots, not a grey lozenge.
final class HoverView: NSView {
    var onHover: (Bool) -> Void = { _ in }
    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        trackingAreas.forEach(removeTrackingArea)
        addTrackingArea(NSTrackingArea(rect: bounds,
                                       options: [.mouseEnteredAndExited, .activeAlways],
                                       owner: self, userInfo: nil))
    }
    override func mouseEntered(with event: NSEvent) { onHover(true) }
    override func mouseExited(with event: NSEvent) { onHover(false) }
}

// ---------------------------------------------------------------- controller

final class Overlay {
    private var windowID: CGWindowID = 0
    private var comments: [Comment] = []
    private var expanded = false
    private var panel: OverlayPanel!
    private var container: HoverView!   // hover tracking + content host
    private var fx: NSVisualEffectView! // blurred background only
    private var hovering = false
    private var occluded = false
    private var occlusionTick = 0
    var corner: Corner = .tr
    var placement: Placement = .float
    private var pill: PillView!
    private var stroke: ShapeStrokeView!
    private var scrim: ScrimView!
    private var shadow: SoftShadowView!
    private var card: CardView?
    /// Set when this badge needs 60Hz; the manager runs fast if ANY badge does.
    var wantsFast = true
    /// Told to the manager when the target window goes away for good.
    var onClosed: ((CGWindowID) -> Void)?
    private var lastTargetTitle = ""

    init() {
        panel = OverlayPanel(contentRect: NSRect(x: 0, y: 0, width: 60, height: pillHeight),
                             styleMask: [.borderless, .nonactivatingPanel],
                             backing: .buffered, defer: false)
        panel.isFloatingPanel = true
        panel.level = .floating
        panel.hidesOnDeactivate = false
        panel.isReleasedWhenClosed = false
        panel.isOpaque = false
        panel.backgroundColor = .clear
        // In float placement we draw our own shadow, and letting the window server
        // also compute one meant calling invalidateShadow() on every frame of a
        // resize — which is exactly the flicker seen while the card opens.
        panel.hasShadow = false
        // .stationary keeps Mission Control / Spaces from relocating the panel
        // behind our back, which otherwise leaves it somewhere else on return and
        // makes the follow glide it back in as if it were re-opening.
        panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary,
                                    .ignoresCycle, .stationary]
        panel.animationBehavior = .none
        // Pin to dark regardless of system theme: this is a HUD that has to read
        // the same over a white Xcode window and a black terminal, and the text
        // colours below are all light-on-dark.
        panel.appearance = NSAppearance(named: .darkAqua)

        container = HoverView(frame: panel.contentLayoutRect)
        container.autoresizingMask = [.width, .height]
        container.onHover = { [weak self] inside in
            self?.hovering = inside
            self?.updateAlpha(animated: true)
        }
        panel.contentView = container

        fx = NSVisualEffectView(frame: container.bounds)
        fx.material = .hudWindow
        fx.blendingMode = .behindWindow
        fx.state = .active
        fx.wantsLayer = true
        fx.layer?.cornerRadius = pillHeight / 2
        fx.layer?.masksToBounds = true
        fx.layer?.borderWidth = 1
        fx.layer?.borderColor = NSColor(white: 1, alpha: 0.13).cgColor
        fx.autoresizingMask = [.width, .height]
        fx.alphaValue = restAlpha
        container.addSubview(fx)

        // The material alone sits at whatever contrast the window behind it
        // happens to have — over a white browser it went milky and the white text
        // lost legibility. A fixed scrim pins the floor.
        scrim = ScrimView(frame: fx.bounds)
        scrim.autoresizingMask = [.width, .height]
        fx.addSubview(scrim)

        shadow = SoftShadowView(frame: container.bounds)
        shadow.autoresizingMask = [.width, .height]
        shadow.isHidden = true
        container.addSubview(shadow, positioned: .below, relativeTo: fx)

        stroke = ShapeStrokeView(frame: container.bounds)
        stroke.autoresizingMask = [.width, .height]
        stroke.isHidden = true
        container.addSubview(stroke)

        pill = PillView(frame: container.bounds)
        pill.onClick = { [weak self] in self?.toggle() }
        container.addSubview(pill)
    }

    func attach(_ wid: CGWindowID) {
        windowID = wid
        if let i = windowInfo(wid) {
            lastTargetTitle = i.title.isEmpty ? i.owner : i.title
        }
        wantsFast = true
        rebuild()
        panel.orderFrontRegardless()
    }

    /// Called once per tick by OverlayManager, which owns the only timer. Returns
    /// whether this badge still needs the fast cadence.
    @discardableResult
    func tick(_ now: CFTimeInterval) -> Bool {
        reposition(now)
        return wantsFast
    }

    /// Tear down this badge's window without touching the process.
    func close() {
        panel.orderOut(nil)
        comments.removeAll()
    }

    func upsert(_ c: Comment) {
        if let i = comments.firstIndex(where: { $0.id == c.id }) {
            comments[i] = c
        } else {
            comments.append(c)
            evictIfOverCap()
        }
        rebuild()
    }

    /// The list is agent-driven, so it is only as bounded as the agent is. An
    /// agent posting a note per failing test would otherwise grow it without
    /// limit — and the card builds a row view per comment, so the cost is real
    /// memory, not just a long scroll.
    ///
    /// Eviction prefers to drop something the user does not owe anything on:
    /// oldest resolved/ambient note first, and only falls back to dropping an
    /// oldest `attention` when every single one is blocking.
    private func evictIfOverCap() {
        while comments.count > maxComments {
            let victim = comments.enumerated()
                .filter { !$0.element.status.pulses }
                .min(by: { $0.element.created < $1.element.created })?.offset
                ?? comments.enumerated()
                    .min(by: { $0.element.created < $1.element.created })!.offset
            let dropped = comments.remove(at: victim)
            // Tell the daemon, so its copy stays in step with what is on screen.
            emit(["event": "evicted", "id": dropped.id, "window": Int(windowID)])
        }
    }
    func remove(_ id: String) {
        comments.removeAll { $0.id == id }
        if comments.isEmpty { expanded = false }
        rebuild()
    }
    func clear() { comments.removeAll(); expanded = false; rebuild() }

    private func toggle() { setExpanded(!expanded) }

    /// Exposed on the protocol too: an agent posting something urgent may want
    /// the card already open rather than a dot the user has to notice.
    func setExpanded(_ v: Bool) {
        guard !comments.isEmpty else { return }
        guard v != expanded else { return }
        expanded = v
        if expanded { emit(["event": "expand", "window": Int(windowID)]) }
        rebuild(animated: true)
    }

    /// Rebuild content and start (or skip) the pill↔card morph. Nothing
    /// geometric happens here: every frame of the animation is resolved inside
    /// `reposition`, which the 60Hz tracker already drives. That is the whole
    /// trick — an NSAnimationContext frame animation and a per-tick
    /// setFrameOrigin fight each other, and the window-follow always wins.
    private func rebuild(animated: Bool = false) {
        if comments.isEmpty { setVisible(false); return }
        pill.comments = comments
        if expanded {
            // Rebuilt rather than patched so edited/added comments show up; the
            // per-frame layout re-applies alpha, so no state is lost mid-morph.
            card?.removeFromSuperview()
            buildCard()
        } else if !animated {
            card?.removeFromSuperview(); card = nil
        }
        setExpansion(expanded ? 1 : 0, animated: animated)
        updateAlpha(animated: animated)
        if !occluded { setVisible(true) }
        reposition()
    }

    private func buildCard() {
        // Newest first: the list is capped at three visible rows, so the most
        // recent has to be the one you see without scrolling.
        let ordered = comments.sorted { $0.created > $1.created }
        let c = CardView(comments: ordered, target: lastTargetTitle,
                         onHandback: { [weak self] id in self?.handback(id) },
                         onDismiss: { [weak self] id in
                             self?.emit(["event": "dismiss", "id": id,
                                         "window": Int(self?.windowID ?? 0)])
                             self?.remove(id)
                         },
                         onDismissAll: { [weak self] in
                             guard let self = self else { return }
                             for c in self.comments {
                                 self.emit(["event": "dismiss", "id": c.id,
                                            "window": Int(self.windowID)])
                             }
                             self.clear()
                         },
                         onCollapse: { [weak self] in self?.toggle() })
        c.alphaValue = 0
        // Inside the effect view so its rounded corners clip the reveal.
        fx.addSubview(c)
        card = c
    }

    // ---- expansion animation (0 = pill, 1 = card) ----

    private func setExpansion(_ v: CGFloat, animated: Bool) {
        if animated {
            animFrom = expansion; animTo = v
            animStart = CACurrentMediaTime(); animating = true
        } else {
            animating = false
            expansion = v
            if v == 0 { card?.removeFromSuperview(); card = nil }
        }
    }

    private func tickExpansion() {
        guard animating else { return }
        let t = min(1, CGFloat((CACurrentMediaTime() - animStart) / animDuration))
        // easeOutCubic: leaves quickly, lands softly — the shape people read as
        // "physical" rather than "timed".
        let e = 1 - pow(1 - t, 3)
        expansion = animFrom + (animTo - animFrom) * e
        guard t >= 1 else { return }
        animating = false
        expansion = animTo
        if !expanded { card?.removeFromSuperview(); card = nil }
    }

    /// The notch silhouette applies only to the resting tab. The expanded card is
    /// a big surface — hanging it above the window would push it off-screen or
    /// over the menu bar — so it drops down inside the window as a plain rounded
    /// panel, reading as a drawer pulled out of the tab.
    /// Padding depends on placement only, never on expansion — keeping it
    /// constant means the card inherits the same drawn shadow as the pill and the
    /// morph has no discontinuity to hide.
    private var pad: CGFloat { placement == .float ? floatPad : 0 }

    /// The pill's content size for the current placement.
    private var pillInner: NSSize {
        let bodyW = PillView.width(for: comments)
        if placement == .notch {
            return NSSize(width: bodyW + notchFillet * 2, height: notchHeight)
        }
        return NSSize(width: bodyW, height: pillHeight)
    }

    /// Resolve every frame of the morph: geometry, silhouette and the two content
    /// layers' opacities, all from one 0…1 value.
    private func layoutContents(inner: NSSize, e: CGFloat) {
        fx.frame = NSRect(x: pad, y: pad, width: inner.width, height: inner.height)
        scrim.frame = fx.bounds
        scrim.glass = placement == .float

        // The notch silhouette only holds at rest; past a hair of expansion the
        // shape becomes the rounded card.
        let asNotch = placement == .notch && e < 0.02
        let r = min(inner.height / 2, pillHeight / 2 + (16 - pillHeight / 2) * e)
        fx.layer?.borderWidth = 0
        if asNotch {
            fx.layer?.cornerRadius = 0
            fx.maskImage = notchMask(fx.bounds.size)
            let p = notchPath(fx.bounds.size)
            scrim.shape = p
            stroke.path = p
        } else {
            // Only clear it when it is actually set — reassigning maskImage makes
            // the effect view re-render its material, which flickers on resize.
            if fx.maskImage != nil { fx.maskImage = nil }
            fx.layer?.cornerRadius = r
            scrim.shape = NSBezierPath(roundedRect: fx.bounds, xRadius: r, yRadius: r)
            // Hairline just inside the edge so it reads as a rim, not an outline.
            stroke.path = NSBezierPath(roundedRect: fx.bounds.insetBy(dx: 0.5, dy: 0.5),
                                       xRadius: r, yRadius: r)
        }
        stroke.frame = fx.frame
        stroke.isHidden = false
        stroke.needsDisplay = true
        scrim.needsDisplay = true

        if placement == .float {
            shadow.frame = container.bounds
            shadow.shape = NSBezierPath(roundedRect: fx.frame, xRadius: r, yRadius: r)
            shadow.isHidden = false
            shadow.needsDisplay = true
        } else {
            shadow.isHidden = true
        }

        // Dots leave fast, card arrives a beat later — a straight crossfade makes
        // the middle of the morph look like mud.
        pill.frame = NSRect(x: pad + (asNotch ? notchFillet : 0),
                            y: pad + (asNotch ? notchTuck : 0),
                            width: inner.width - (asNotch ? notchFillet * 2 : 0),
                            height: inner.height - (asNotch ? notchTuck : 0))
        pill.alphaValue = max(0, 1 - e / 0.30)
        pill.isHidden = pill.alphaValue <= 0.01
        if let c = card {
            let h = c.frame.height
            // Pinned to the anchored corner so the card unfolds out of the pill
            // rather than sliding in from somewhere else.
            c.frame = NSRect(x: fx.bounds.width - cardWidth, y: fx.bounds.height - h,
                             width: cardWidth, height: h)
            // Deliberately late: the card is laid out at full size and clipped by
            // the growing panel, so early opacity shows sentences sliced through
            // the middle. Holding it back until the panel is most of the way there
            // turns the reveal into the card materialising rather than unmasking.
            c.alphaValue = max(0, min(1, (e - 0.40) / 0.45))
        }
    }

    /// Only the background chrome fades — the dots stay at full strength so the
    /// resting badge reads as colour, not as a grey lozenge.
    private func updateAlpha(animated: Bool) {
        let target: CGFloat = (expanded || hovering) ? 1.0 : restAlpha
        guard animated else { fx.alphaValue = target; return }
        NSAnimationContext.runAnimationGroup { ctx in
            ctx.duration = 0.16
            ctx.timingFunction = CAMediaTimingFunction(name: .easeOut)
            fx.animator().alphaValue = target
        }
    }

    private var lastInner: NSSize = .zero
    private var lastExpansion: CGFloat = -1
    private var lastActivity: CFTimeInterval = 0
    /// Smoothed follow state. `renderOrigin` trails the computed target.
    private var renderOrigin: NSPoint = .zero
    private var hasRendered = false
    private var inSetVisible = false
    /// When the target first went missing from the window list, 0 if present.
    private var missingSince: CFTimeInterval = 0
    private var lastTick: CFTimeInterval = CACurrentMediaTime()
    /// Time constant of the follow. ~90ms reads as "attached but relaxed"; lower
    /// gets twitchy on a coarse sample stream, higher feels like drag.
    private let followTau: Double =
        ProcessInfo.processInfo.environment["REMINAL_OVERLAY_TAU"].flatMap(Double.init) ?? 0.09
    private var expansion: CGFloat = 0
    private var animFrom: CGFloat = 0
    private var animTo: CGFloat = 0
    private var animStart: CFTimeInterval = 0
    private var animating = false
    /// Overridable so the morph can be slowed right down and inspected frame by
    /// frame; a 0.26s animation is far shorter than a screenshot round-trip.
    private let animDuration: CFTimeInterval =
        ProcessInfo.processInfo.environment["REMINAL_OVERLAY_ANIM"].flatMap(Double.init) ?? 0.26

    /// Where the panel sits for a given size. `resting: true` is the pill in its
    /// chosen placement; `resting: false` is the expanded card, which always sits
    /// inside the window's corner.
    private func anchorOrigin(size: NSSize, resting: Bool, ns: NSRect) -> NSPoint {
        let contentW = size.width - pad * 2, contentH = size.height - pad * 2
        let insideOrigin = NSPoint(
            x: corner.right ? ns.maxX - inset - contentW - pad : ns.minX + inset - pad,
            y: corner.top ? ns.maxY - inset - contentH - pad : ns.minY + inset - pad)
        guard resting else { return insideOrigin }

        switch placement {
        case .inside:
            return insideOrigin
        case .notch:
            var o = NSPoint(x: corner.right ? ns.maxX - notchCornerInset - contentW
                                            : ns.minX + notchCornerInset,
                            y: ns.maxY - notchTuck)
            let scr = NSScreen.screens.first(where: { $0.frame.intersects(ns) }) ?? NSScreen.main
            if let f = scr?.visibleFrame, o.y + size.height > f.maxY { o = insideOrigin }
            return o
        case .float:
            // A clear gap of air above the top edge, inset from the corner so it
            // lines up with the window's straight run rather than its curve.
            var o = NSPoint(x: (corner.right ? ns.maxX - notchCornerInset - contentW
                                             : ns.minX + notchCornerInset) - pad,
                            y: ns.maxY + floatGap - pad)
            let scr = NSScreen.screens.first(where: { $0.frame.intersects(ns) }) ?? NSScreen.main
            if let f = scr?.visibleFrame, o.y + size.height - pad > f.maxY {
                // No air above (window under the menu bar) — tuck it inside.
                o = insideOrigin
            }
            return o
        }
    }

    /// Anchor to the target window's top-right, and follow it. Hiding when the
    /// window goes off-screen (minimised, app hidden, other Space) is what keeps
    /// a floating panel from looking like a stuck artifact.
    private func reposition(_ now: CFTimeInterval = CACurrentMediaTime()) {
        guard let info = windowInfo(windowID) else {
            // The window is not in the window list. That is USUALLY it being
            // closed — comments die with the window, by design — but the list can
            // also blink during Space switches and Mission Control, so require the
            // absence to persist before declaring it gone.
            setVisible(false)
            if missingSince == 0 { missingSince = now }
            if now - missingSince > 0.6 {
                emit(["event": "closed", "window": Int(windowID)])
                comments.removeAll()
                wantsFast = false
                // The process serves many windows now, so losing one is not the
                // end of it — hand back to the manager, which drops this panel.
                onClosed?(windowID)
            }
            return
        }
        missingSince = 0
        // A minimised or hidden window is NOT closed: keep the list, just hide.
        if !info.onscreen {
            // Minimised, app hidden, or another Space: fade out and, crucially,
            // forget the rendered position so returning snaps instead of gliding.
            setVisible(false)
            hasRendered = false
            return
        }
        if !info.title.isEmpty { lastTargetTitle = info.title }
        let ns = cgToNS(info.bounds)

        // Advance the morph, then derive this frame's geometry from it. Size and
        // position are applied together in one setFrame so the two never race.
        tickExpansion()
        let e = expansion
        let from = pillInner
        let to = NSSize(width: cardWidth, height: card?.frame.height ?? from.height)
        let inner = NSSize(width: from.width + (to.width - from.width) * e,
                           height: from.height + (to.height - from.height) * e)
        let size = NSSize(width: inner.width + pad * 2, height: inner.height + pad * 2)

        // The pill anchors to its placement (floating above the edge) while the
        // card always sits inside the window — it is far too big to hang off an
        // edge near a screen bound. Interpolating between the two anchors instead
        // of switching between them is what stops the badge jumping mid-morph.
        let pillSize = NSSize(width: from.width + pad * 2, height: from.height + pad * 2)
        let cardSize = NSSize(width: to.width + pad * 2, height: to.height + pad * 2)
        let o0 = anchorOrigin(size: pillSize, resting: true, ns: ns)
        let o1 = anchorOrigin(size: cardSize, resting: false, ns: ns)
        let target = NSPoint(x: o0.x + (o1.x - o0.x) * e, y: o0.y + (o1.y - o0.y) * e)

        // Don't chase the window sample-for-sample — that is what looked jittery,
        // because the samples arrive on an unsynced timer while the display runs
        // at up to 120Hz. Instead ease toward the target with a fixed time
        // constant. The filter emits continuous motion regardless of how coarse
        // or irregular the samples are, so the badge glides and simply settles a
        // beat after the window stops. Lag is deliberate; smoothness is the point.
        let dt = min(0.25, max(0.001, now - lastTick))
        lastTick = now
        if !hasRendered || hypot(target.x - renderOrigin.x, target.y - renderOrigin.y) > 400 {
            // First placement, a reappearance, or a jump so large that easing it
            // would read as the badge flying across the screen — snap instead.
            renderOrigin = target
            hasRendered = true
        } else {
            // Frame-rate independent: same feel at 12Hz idle or 60Hz active.
            let k = 1 - exp(-dt / followTau)
            renderOrigin = NSPoint(x: renderOrigin.x + (target.x - renderOrigin.x) * k,
                                   y: renderOrigin.y + (target.y - renderOrigin.y) * k)
            if abs(target.x - renderOrigin.x) < 0.3, abs(target.y - renderOrigin.y) < 0.3 {
                renderOrigin = target      // settle exactly, then stop working
            }
        }
        let origin = NSPoint(x: renderOrigin.x.rounded(), y: renderOrigin.y.rounded())
        let settled = renderOrigin == target
        let frame = NSRect(origin: origin, size: size)
        if debugLayout, animating || !settled {
            FileHandle.standardError.write(Data(String(
                format: "e=%.2f target=(%.0f,%.0f) render=(%.1f,%.1f) gap=%.1f\n",
                e, target.x, target.y, renderOrigin.x, renderOrigin.y,
                hypot(target.x - renderOrigin.x, target.y - renderOrigin.y)).utf8))
        }
        // Redraw only when the geometry actually changed. Following a window is
        // overwhelmingly a pure translation, and re-rendering the blurred shadow,
        // the gradient scrim and the stroke on every one of 60 ticks per second
        // for an unchanged shape costs several percent of a core continuously.
        let sizeChanged = abs(inner.width - lastInner.width) > 0.01
            || abs(inner.height - lastInner.height) > 0.01
            || abs(e - lastExpansion) > 0.001
        if sizeChanged {
            // display:false — the subsequent layout marks exactly the layers that
            // changed, so forcing a synchronous full redraw here only adds tearing.
            panel.setFrame(frame, display: false)
            layoutContents(inner: inner, e: e)
            lastInner = inner
            lastExpansion = e
        } else if panel.frame.origin != origin {
            // Pure move: the contents are identical, so don't mark them dirty.
            panel.setFrameOrigin(origin)
        }

        // Idle badges shouldn't hold the CPU at 60Hz. Run fast only while there is
        // motion to render — an unsettled follow or a running expand — and fall
        // back to a lazy poll that is only looking for the window to move again.
        if !settled || animating { lastActivity = now }
        wantsFast = !settled || animating || now - lastActivity < 0.4

        // Occlusion at ~15Hz: cheap enough, and a badge only has to disappear as
        // fast as the eye notices the window went behind something.
        // Every other tick: 30Hz while active, 6Hz at rest. Mission Control has to
        // be noticed inside its own open animation, so this can't be too lazy.
        occlusionTick &+= 1
        if occlusionTick % 2 == 0 { updateOcclusion() }

        if !panel.isVisible && !comments.isEmpty && !occluded { setVisible(true) }
    }

    /// A .floating panel is above *everything*, so without this the badge keeps
    /// hovering over whatever window buried its target — which makes it
    /// impossible to tell which window a badge belongs to. Walk the front-to-back
    /// window list; if anything ahead of the target overlaps the badge, hide.
    private func updateOcclusion() {
        // Shared across every badge: with a dozen panels this enumeration was
        // being repeated a dozen times a tick for an identical answer.
        let arr = WindowList.onScreen()
        let badge = nsToCG(panel.frame)
        let screen = primaryScreen()?.frame ?? .zero
        var dockFullScreenWindows = 0
        // Testing the badge rect alone was wrong: a floating pill sits OUTSIDE
        // its window, so another app covering the window need not overlap the
        // pill — the badge stayed up over a buried window, and only the much
        // larger open card ever intersected anything. Also probe the window's own
        // anchor corner, which is the thing the badge actually belongs to.
        var probe = CGRect.null
        if let info = windowInfo(windowID) {
            let b = info.bounds   // CG: y grows downward, so the top edge is minY
            let w: CGFloat = 90, h: CGFloat = 44
            probe = CGRect(x: corner.right ? b.maxX - w : b.minX,
                           y: corner.top ? b.minY : b.maxY - h,
                           width: w, height: h)
        }
        let mypid = ProcessInfo.processInfo.processIdentifier
        var covered = false
        for d in arr {
            let layerAny = d[kCGWindowLayer as String] as? Int ?? -1
            let rect: CGRect? = (d[kCGWindowBounds as String] as? NSDictionary)
                .flatMap { CGRect(dictionaryRepresentation: $0) }
            // Mission Control has no public API. Measured: with it closed the Dock
            // owns exactly ONE screen-sized window above layer 0 (its own backing
            // window, named "Dock"); while it is up there are three — two extra
            // unnamed ones at layers 20 and 18. Counting is deliberate: keying on
            // the empty name would break wherever kCGWindowName is unpopulated
            // (it needs the screen-recording grant), and keying on layer 18 would
            // break if Apple renumbers. MC also rescales every window's reported
            // bounds, so the badge would otherwise chase a shrunken thumbnail —
            // hiding is both what the user asked for and what keeps the follow sane.
            if (d[kCGWindowOwnerName as String] as? String) == "Dock", layerAny > 0,
               let r = rect, r.width >= screen.width * 0.9, r.height >= screen.height * 0.9 {
                dockFullScreenWindows += 1
            }
            guard let wid = d[kCGWindowNumber as String] as? CGWindowID else { continue }
            if wid == windowID { break }   // reached the target: everything after is behind it
            // Only normal app windows can occlude. This also skips our own panel,
            // which lives above layer 0 and would otherwise always "cover" itself.
            guard let layer = d[kCGWindowLayer as String] as? Int, layer == 0,
                  let pid = d[kCGWindowOwnerPID as String] as? Int32, pid != mypid,
                  let bd = d[kCGWindowBounds as String] as? NSDictionary,
                  let r = CGRect(dictionaryRepresentation: bd) else { continue }
            if r.intersects(badge) || (!probe.isNull && r.intersects(probe)) {
                // Safe to stop: Dock's high-layer windows sort ahead of layer 0,
                // so they are already counted by the time any occluder is found.
                covered = true; break
            }
        }
        let missionControl = dockFullScreenWindows >= 2
        let hide = covered || missionControl
        guard hide != occluded else { return }
        occluded = hide
        if debugLayout {
            FileHandle.standardError.write(Data("hidden=\(hide) mc=\(missionControl)\n".utf8))
        }
        setVisible(!hide && !comments.isEmpty)
    }

    /// Show/hide with a short fade. An orderOut with no fade is the "vanishes in
    /// a flash" — and re-showing snaps to position rather than gliding in, so a
    /// badge returning from Mission Control or from behind a window doesn't look
    /// like it is re-opening.
    private func setVisible(_ v: Bool) {
        // reposition() shows the panel when it finds it hidden, and showing it
        // repositions — without this guard those two call each other until the
        // stack runs out (a real SIGSEGV, caught in testing).
        guard !inSetVisible else { return }
        inSetVisible = true
        defer { inSetVisible = false }

        if v {
            guard !panel.isVisible else { return }
            hasRendered = false           // snap on reappear, never glide in
            panel.alphaValue = 0
            // Order front BEFORE repositioning: once isVisible is true the
            // show-if-hidden branch in reposition() is inert.
            panel.orderFrontRegardless()
            reposition()
            NSAnimationContext.runAnimationGroup { ctx in
                ctx.duration = 0.14
                panel.animator().alphaValue = 1
            }
        } else {
            guard panel.isVisible else { return }
            NSAnimationContext.runAnimationGroup({ ctx in
                ctx.duration = 0.12
                panel.animator().alphaValue = 0
            }, completionHandler: { [weak self] in
                guard let self = self, self.panel.alphaValue < 0.02 else { return }
                self.panel.orderOut(nil)
            })
        }
    }

    private func handback(_ id: String) {
        emit(["event": "handback", "id": id, "window": Int(windowID)])
        // Local echo so the prototype demonstrates the round trip without a
        // daemon: the row flips to "back to agent" and settles.
        if let i = comments.firstIndex(where: { $0.id == id }) {
            comments[i].status = .handback
            comments[i].created = Date()
            rebuild()
        }
    }

    private func emit(_ obj: [String: Any]) {
        guard let d = try? JSONSerialization.data(withJSONObject: obj),
              var s = String(data: d, encoding: .utf8) else { return }
        s += "\n"
        FileHandle.standardOutput.write(Data(s.utf8))
    }
}

// ---------------------------------------------------------------- line protocol

/// Owns every badge in the process and the single timer that drives them all.
///
/// One process, many panels — the same shape reminal-capture's `serve` mode
/// uses. Before this each window meant its own process, its own timer and its
/// own window-list walk.
final class OverlayManager {
    static let shared = OverlayManager()

    private var overlays: [CGWindowID: Overlay] = [:]
    private var timer: Timer?
    private var fast = false
    /// demo/one-shot use: leave when the last badge's window closes.
    var exitWhenEmpty = false

    var count: Int { overlays.count }
    func overlay(for wid: CGWindowID) -> Overlay? { overlays[wid] }

    /// The single overlay when exactly one is attached — lets commands that
    /// omit a window id keep working for single-window callers.
    var soleOverlay: Overlay? { overlays.count == 1 ? overlays.values.first : nil }

    @discardableResult
    func attach(_ wid: CGWindowID, corner: Corner, placement: Placement) -> Overlay {
        if let existing = overlays[wid] { return existing }
        let o = Overlay()
        o.corner = corner
        o.placement = placement
        o.onClosed = { [weak self] id in self?.drop(id) }
        overlays[wid] = o
        o.attach(wid)
        setRate(fast: true)
        return o
    }

    func drop(_ wid: CGWindowID) {
        guard let o = overlays.removeValue(forKey: wid) else { return }
        o.close()
        if overlays.isEmpty {
            timer?.invalidate()
            timer = nil
            if exitWhenEmpty { NSApp.terminate(nil) }
        }
    }

    func closeAll() {
        for (_, o) in overlays { o.close() }
        overlays.removeAll()
        timer?.invalidate()
        timer = nil
    }

    private func tick() {
        let now = CACurrentMediaTime()
        var wantFast = false
        // Snapshot the values: a badge may drop itself mid-tick when its window
        // closes, and mutating the dictionary while iterating it would trap.
        for o in Array(overlays.values) where o.tick(now) { wantFast = true }
        setRate(fast: wantFast)
    }

    /// 60Hz while anything is moving or animating, 12Hz once everything settles.
    private func setRate(fast wanted: Bool) {
        guard wanted != fast || timer == nil else { return }
        fast = wanted
        timer?.invalidate()
        guard !overlays.isEmpty else { timer = nil; return }
        let t = Timer(timeInterval: wanted ? 1.0 / 60.0 : 1.0 / 12.0, repeats: true) {
            [weak self] _ in self?.tick()
        }
        RunLoop.main.add(t, forMode: .common)
        timer = t
    }
}

/// Route one protocol line. Every command may name a `window`; commands that
/// omit it fall back to the sole attached badge, so single-window callers (and
/// the demo) keep working unchanged.
func handle(_ line: String) {
    guard let d = line.data(using: .utf8),
          let obj = try? JSONSerialization.jsonObject(with: d) as? [String: Any],
          let cmd = obj["cmd"] as? String else { return }
    let mgr = OverlayManager.shared

    if cmd == "quit" {
        mgr.closeAll()
        NSApp.terminate(nil)
        return
    }

    if cmd == "attach" {
        guard let w = obj["window"] as? Int else { return }
        let corner = (obj["corner"] as? String).flatMap(Corner.init(rawValue:)) ?? .tr
        let placement = (obj["placement"] as? String).flatMap(Placement.init(rawValue:)) ?? .float
        mgr.attach(CGWindowID(w), corner: corner, placement: placement)
        return
    }

    // Everything else needs to know which badge it is talking to.
    let target: Overlay?
    if let w = obj["window"] as? Int {
        target = mgr.overlay(for: CGWindowID(w))
    } else {
        target = mgr.soleOverlay
    }
    guard let overlay = target else {
        FileHandle.standardError.write(Data(
            "no overlay for that window (attach first, or name a window)\n".utf8))
        return
    }

    switch cmd {
    case "upsert":
        overlay.upsert(Comment(id: obj["id"] as? String ?? UUID().uuidString,
                               status: Status(rawValue: obj["status"] as? String ?? "info") ?? .info,
                               title: obj["title"] as? String ?? "",
                               body: obj["body"] as? String ?? "",
                               author: obj["author"] as? String ?? "agent",
                               created: Date()))
    case "remove":
        if let id = obj["id"] as? String { overlay.remove(id) }
    case "clear":
        overlay.clear()
    case "detach":
        // Drop the badge but keep serving other windows.
        if let w = obj["window"] as? Int { mgr.drop(CGWindowID(w)) }
    case "expand":
        overlay.setExpanded(true)
    case "collapse":
        overlay.setExpanded(false)
    default:
        break
    }
}

// ---------------------------------------------------------------- main

let args = CommandLine.arguments

// `dump` lists every on-screen window with its layer and owner — used to work out
// how to recognise Mission Control, which has no public API.
if args.count >= 2, args[1] == "dump" {
    if let arr = CGWindowListCopyWindowInfo([.optionOnScreenOnly], kCGNullWindowID) as? [[String: Any]] {
        for d in arr {
            let owner = d[kCGWindowOwnerName as String] as? String ?? "?"
            let name = d[kCGWindowName as String] as? String ?? ""
            let layer = d[kCGWindowLayer as String] as? Int ?? -999
            var box = ""
            if let bd = d[kCGWindowBounds as String] as? NSDictionary,
               let r = CGRect(dictionaryRepresentation: bd) {
                box = "\(Int(r.width))x\(Int(r.height))@\(Int(r.minX)),\(Int(r.minY))"
            }
            print("layer=\(layer)\t\(owner)\t\(name)\t\(box)")
        }
    }
    exit(0)
}

if args.count >= 2, args[1] == "windows" {
    for (wid, i) in candidateWindows() {
        let t = i.title.isEmpty ? "—" : i.title
        print("\(wid)\t\(i.owner)\t\(t)\t\(Int(i.bounds.width))x\(Int(i.bounds.height))"
              + "@\(Int(i.bounds.minX)),\(Int(i.bounds.minY))")
    }
    exit(0)
}

let app = NSApplication.shared
app.setActivationPolicy(.accessory)   // no Dock icon, no menu bar — it is furniture

if args.count >= 2, args[1] == "demo" {
    var target: CGWindowID? = args.count >= 3 ? CGWindowID(args[2]) : nil
    if target == nil { target = candidateWindows().first?.id }
    var corner = Corner.tr
    var placement = Placement.float
    for a in args.dropFirst(2) {
        if let c = Corner(rawValue: a) { corner = c }
        if let p = Placement(rawValue: a) { placement = p }
    }
    guard let wid = target else {
        FileHandle.standardError.write(Data("no candidate window found\n".utf8)); exit(1)
    }
    // Demo is a one-shot: when its window goes, so does the process, so a test
    // run never leaves a stray badge behind.
    OverlayManager.shared.exitWhenEmpty = true
    let overlay = OverlayManager.shared.attach(wid, corner: corner, placement: placement)
    overlay.upsert(Comment(id: "c1", status: .attention,
                           title: "Signing cert expired",
                           body: "Renew the Developer ID cert in Keychain, then hand this back and I'll re-run the release build.",
                           author: "claude", created: Date().addingTimeInterval(-140)))
    overlay.upsert(Comment(id: "c2", status: .working,
                           title: "Running the test suite",
                           body: "142 of 380 done — no failures yet.",
                           author: "codex", created: Date().addingTimeInterval(-20)))
    overlay.upsert(Comment(id: "c3", status: .info,
                           title: "Left a note in build.sh",
                           body: "", author: "claude", created: Date().addingTimeInterval(-900)))
    FileHandle.standardError.write(Data("attached to window \(wid) — click the badge\n".utf8))
    // `autoexpand` opens the card a beat after launch, so the morph can be
    // captured without a control channel — driving stdin from a FIFO blocks the
    // process from starting at all until a writer attaches.
    if args.contains("autoexpand") {
        let t = Timer(timeInterval: 1.5, repeats: false) { _ in overlay.setExpanded(true) }
        RunLoop.main.add(t, forMode: .common)
    }
}

// stdin drives every badge; EOF is not fatal, so a tty run stays up.
DispatchQueue.global(qos: .utility).async {
    while let line = readLine(strippingNewline: true) {
        DispatchQueue.main.async { handle(line) }
    }
}

app.run()
