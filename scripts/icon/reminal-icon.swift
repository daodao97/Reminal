import AppKit

// reminal "re" macOS app icon. Same mark as the web-viewer favicon (bg #0d1117,
// "re" in Menlo-Bold #58a6ff), but laid out on the macOS app-icon GRID: the
// rounded-rect body is ~80.5% of the canvas, centered, with transparent margin
// so it matches the visual size of other app icons (a full-bleed body reads as
// oversized next to them).
let bg = NSColor(srgbRed: 0x0d/255.0, green: 0x11/255.0, blue: 0x17/255.0, alpha: 1)
let fg = NSColor(srgbRed: 0x58/255.0, green: 0xa6/255.0, blue: 0xff/255.0, alpha: 1)

func render(_ size: Int, _ path: String) {
    let s = CGFloat(size)
    let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: size, pixelsHigh: size,
        bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
        colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    rep.size = NSSize(width: size, height: size)
    NSGraphicsContext.saveGraphicsState()
    let ctx = NSGraphicsContext(bitmapImageRep: rep)!
    NSGraphicsContext.current = ctx

    NSColor.clear.set()
    NSRect(x: 0, y: 0, width: s, height: s).fill()

    // Apple macOS icon grid: 824/1024 body (~9.77% margin per side),
    // corner radius ~22.37% of the body width.
    let inset = s * 0.0977
    let bodyRect = NSRect(x: inset, y: inset, width: s - 2 * inset, height: s - 2 * inset)
    let bodyW = bodyRect.width
    let radius = bodyW * 0.2237
    let rr = NSBezierPath(roundedRect: bodyRect, xRadius: radius, yRadius: radius)
    bg.setFill(); rr.fill()

    // "re" sized relative to the BODY (not the full canvas), centered on the
    // glyph ink box (not the loose line box).
    let fontSize = bodyW * 0.52
    let font = NSFont(name: "Menlo-Bold", size: fontSize)
        ?? NSFont.monospacedSystemFont(ofSize: fontSize, weight: .bold)
    let attrs: [NSAttributedString.Key: Any] = [.font: font, .foregroundColor: fg]
    let str = NSAttributedString(string: "re", attributes: attrs)
    let line = CTLineCreateWithAttributedString(str)
    let ink = CTLineGetImageBounds(line, ctx.cgContext)
    let x = (s - ink.width) / 2 - ink.minX
    let y = (s - ink.height) / 2 - ink.minY
    str.draw(at: NSPoint(x: x, y: y))

    ctx.flushGraphics()
    NSGraphicsContext.restoreGraphicsState()
    try! rep.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: path))
}

let args = CommandLine.arguments
let outDir = args.count > 1 ? args[1] : "."
if args.count > 2, args[2] == "preview" {
    render(512, "\(outDir)/preview.png")
} else {
    let sizes: [(Int, String)] = [
        (16, "icon_16x16.png"), (32, "icon_16x16@2x.png"),
        (32, "icon_32x32.png"), (64, "icon_32x32@2x.png"),
        (128, "icon_128x128.png"), (256, "icon_128x128@2x.png"),
        (256, "icon_256x256.png"), (512, "icon_256x256@2x.png"),
        (512, "icon_512x512.png"), (1024, "icon_512x512@2x.png"),
    ]
    for (px, name) in sizes { render(px, "\(outDir)/\(name)") }
}
