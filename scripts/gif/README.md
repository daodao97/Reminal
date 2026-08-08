# Docs GIFs

The animated GIFs in the README (`docs/*.gif`) are **rendered HTML scenes**, not
screen recordings. Each scene is a self-contained HTML file styled to a shared
premium look — dark radial-gradient ground, a faint dot grid, glass windows with
soft shadows, device glyphs, dotted connection beams — and animated by a single
JavaScript function that a headless browser drives frame by frame.

This keeps them crisp (captured at 2×), tiny (palette-optimized), reproducible,
and consistent with each other.

## The recipe

1. **Author a scene** — one HTML file (see `own.html` for a full example). Give
   `#stage` a fixed `1120×650` size and the house background, then expose two
   globals for the capture rig:

   ```js
   window.TOTAL = 42;              // number of frames
   window.render = function (t) {  // draw frame t (0 … TOTAL-1); no CSS @keyframes
     // mutate the DOM / styles for tick t
   };
   window.render(0);
   ```

   Keep motion **localized**: animate small elements (a pulsing dot, a streaming
   counter, a status flipping) rather than sliding whole panels. GIF compression
   is frame-to-frame, so a mostly-static scene with a few live bits stays small;
   a full-frame fade or slide bloats it several times over.

2. **Build it:**

   ```bash
   REMINAL_NODE_PATH=<dir-with-playwright> \
     python3 scripts/gif/build.py scripts/gif/own.html docs/own.gif
   ```

   `build.py` serves the scene, runs `capture.js` (headless Chrome at
   `deviceScaleFactor: 2`, calling `window.render(t)` and screenshotting each
   tick), rounds the corners + adds the thin light border (matched to the other
   GIFs), downscales to 1000px, and assembles a palette GIF with ffmpeg.

   Flags: `--fps` (default 10), `--colors` (128), `--width` (1000), `--bayer` (4).
   Aim for ~1 MB — bump `--colors` if you see banding, drop it if the file is
   heavy.

## Requirements

- **playwright** (npm) + **Google Chrome** — `capture.js` launches
  `channel: "chrome"`. Point `NODE_PATH`/`REMINAL_NODE_PATH` at a directory that
  contains the `playwright` module.
- **Pillow** and **ffmpeg** for the border + assembly.

## Style tokens (keep these consistent across scenes)

| role            | value                                                             |
|-----------------|-------------------------------------------------------------------|
| ground          | `radial-gradient(1000px 720px at 50% 30%, #141d2b, #0a1019 54%, #05070c)` |
| accent (blue)   | `#5b9dff`   |
| live/ok (green) | `#45d69a`   |
| window          | `#0b1019`/`#0d1220`, `border:#24304a`, `box-shadow:0 40px 72px rgba(0,0,0,.62)` |
| text / dim      | `#eef3fb` / `#8090a6` |
| mono            | `ui-monospace, 'SF Mono', Menlo, monospace` |
| border (post)   | `rgb(220,223,228)`, ~1px inset, rounded corners on `#0a0d13` |
