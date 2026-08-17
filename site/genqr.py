#!/usr/bin/env python3
"""Generate the scannable QR the hero terminal prints.

Deliberately SVG and not terminal half-block art: half-blocks only stay square
when the character cell is exactly 2:1, and real monospace fonts are ~1.67:1,
which stretches the modules until decoders reject the symbol. SVG modules are
square by construction at any font size or zoom level.
"""
import sys, re, segno

url  = sys.argv[1] if len(sys.argv) > 1 else "https://reminal.app/"
dest = sys.argv[2] if len(sys.argv) > 2 else "public/qr.svg"

q = segno.make(url, error='m')
buf = __import__('io').BytesIO()
q.save(buf, kind='svg', scale=10, border=4, dark='#000000', light='#ffffff',
       xmldecl=False, svgns=True, svgclass=None, lineclass=None)
svg = buf.getvalue().decode('utf-8').strip()

# segno writes a fixed width/height and no viewBox. Sized down by CSS that
# crops the symbol to its top-left corner instead of scaling it — it still
# looks like a QR at a glance, and does not scan. Trade the fixed size for a
# viewBox so CSS can size it freely.
m = re.match(r'<svg([^>]*?)\swidth="(\d+)"\sheight="(\d+)"', svg)
if not m:
    raise SystemExit("unexpected segno svg header — refusing to write a QR that may not scan")
w, h = m.group(2), m.group(3)
svg = svg.replace(f' width="{w}" height="{h}"', f' viewBox="0 0 {w} {h}"', 1)
assert 'viewBox' in svg and 'width=' not in svg.split('>')[0]

open(dest, 'w', encoding='utf-8').write(svg)
print(f"  {dest}   version {q.version}, {len(q.matrix)} modules  ->  {url}")
