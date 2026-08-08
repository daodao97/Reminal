#!/usr/bin/env python3
"""Build an animated docs GIF from an HTML scene, in the house style.

Pipeline: serve the scene → capture N frames in headless Chrome at 2x (capture.js)
→ round the corners + add the light border → assemble a palette-optimized GIF
with ffmpeg. See scripts/gif/README.md for the full recipe and how to author a
new scene.

    python3 scripts/gif/build.py scripts/gif/own.html docs/own.gif
    python3 scripts/gif/build.py scene.html out.gif --fps 10 --colors 128 --width 1000

Requirements: the `playwright` npm package + Google Chrome, Pillow, and ffmpeg.
Point NODE_PATH at a directory containing `playwright` (env var REMINAL_NODE_PATH,
or pass --node-path); the MCP Playwright plugin ships a copy you can reuse.
"""
import argparse, http.server, os, socket, socketserver, subprocess, sys, tempfile, threading
from PIL import Image, ImageDraw

HERE = os.path.dirname(os.path.abspath(__file__))

# Scene stage size — must match #stage in the HTML (kept constant across scenes).
STAGE_W, STAGE_H = 1120, 650


def serve(directory):
    """Serve `directory` on a free localhost port; returns (port, httpd)."""
    handler = lambda *a, **k: http.server.SimpleHTTPRequestHandler(*a, directory=directory, **k)
    httpd = socketserver.TCPServer(("127.0.0.1", 0), handler)
    httpd.RequestHandlerClass.log_message = lambda *a, **k: None  # quiet
    port = httpd.server_address[1]
    threading.Thread(target=httpd.serve_forever, daemon=True).start()
    return port, httpd


def add_border(im, radius=30, border=(220, 223, 228), width=3, inset=2, bg=(10, 13, 19)):
    """Round the frame's corners onto a dark bg and stroke a thin light border —
    applied at native 2x so it stays smooth once downscaled. Matches the other
    docs GIFs (measured off machines.gif)."""
    W, H = im.size
    canvas = Image.new("RGB", (W, H), bg)
    mask = Image.new("L", (W, H), 0)
    ImageDraw.Draw(mask).rounded_rectangle((0, 0, W - 1, H - 1), radius=radius, fill=255)
    canvas.paste(im, (0, 0), mask)
    ImageDraw.Draw(canvas).rounded_rectangle(
        (inset, inset, W - 1 - inset, H - 1 - inset), radius=radius - inset, outline=border, width=width)
    return canvas


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("scene"); ap.add_argument("out")
    ap.add_argument("--fps", type=int, default=10)
    ap.add_argument("--colors", type=int, default=128)
    ap.add_argument("--width", type=int, default=1000)
    ap.add_argument("--bayer", type=int, default=4)
    ap.add_argument("--node-path", default=os.environ.get("REMINAL_NODE_PATH", os.environ.get("NODE_PATH", "")))
    a = ap.parse_args()

    scene = os.path.abspath(a.scene)
    out = os.path.abspath(a.out)
    with tempfile.TemporaryDirectory() as tmp:
        frames, bframes = os.path.join(tmp, "f"), os.path.join(tmp, "b")
        os.makedirs(frames); os.makedirs(bframes)

        port, httpd = serve(os.path.dirname(scene))
        try:
            env = {**os.environ, "NODE_PATH": a.node_path}
            subprocess.run(["node", os.path.join(HERE, "capture.js"),
                            f"http://127.0.0.1:{port}/{os.path.basename(scene)}", frames, str(STAGE_W), str(STAGE_H)],
                           check=True, env=env)
        finally:
            httpd.shutdown()

        for i, name in enumerate(sorted(os.listdir(frames))):
            im = add_border(Image.open(os.path.join(frames, name)).convert("RGB"))
            w, h = im.size
            im.resize((a.width, round(h * a.width / w)), Image.LANCZOS).save(os.path.join(bframes, "f%03d.png" % i))

        vf = f"split[s0][s1];[s0]palettegen=max_colors={a.colors}[p];[s1][p]paletteuse=dither=bayer:bayer_scale={a.bayer}"
        subprocess.run(["ffmpeg", "-y", "-framerate", str(a.fps), "-i", os.path.join(bframes, "f%03d.png"),
                        "-vf", vf, "-loop", "0", out],
                       check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    kb = round(os.path.getsize(out) / 1024)
    print(f"wrote {out}  ({kb} KB)")


if __name__ == "__main__":
    main()
