#!/usr/bin/env bash
# Build the landing page's media from the README's gifs.
#
# The gifs in docs/ are the single source of truth — they're what GitHub
# renders. The web wants the same frames an order of magnitude smaller, so
# each one becomes an H.264 mp4 (played muted+looping, which reads exactly
# like a gif) plus a first-frame poster for the pre-load paint.
#
# Generated files land in public/assets/ and are gitignored: run this before
# `npm run deploy`.
set -euo pipefail

cd "$(dirname "$0")"
SRC=../docs
OUT=public/assets

command -v ffmpeg >/dev/null || { echo "ffmpeg not found — brew install ffmpeg" >&2; exit 1; }

mkdir -p "$OUT"

# Only the scenes the landing page actually uses. The rest stay README-only.
SCENES=(lid hero video setup own)

for name in "${SCENES[@]}"; do
  src="$SRC/$name.gif"
  [ -f "$src" ] || { echo "missing $src" >&2; exit 1; }

  # yuv420p + even dimensions: what every browser and iOS will decode.
  ffmpeg -loglevel error -y -i "$src" \
    -movflags +faststart \
    -pix_fmt yuv420p \
    -vf "scale=trunc(iw/2)*2:trunc(ih/2)*2" \
    -c:v libx264 -preset slow -crf 26 -an \
    "$OUT/$name.mp4"

  # Poster: the first frame, so the slot is painted before the video decodes.
  ffmpeg -loglevel error -y -i "$src" -frames:v 1 -q:v 4 "$OUT/$name.jpg"

  printf "%-8s %6s gif  →  %6s mp4 + %5s poster\n" "$name" \
    "$(du -h "$src" | cut -f1)" \
    "$(du -h "$OUT/$name.mp4" | cut -f1)" \
    "$(du -h "$OUT/$name.jpg" | cut -f1)"
done

cp ../assets/reminal-icon-1024.png "$OUT/icon.png"

# The hero terminal prints a version, so it has to be the real one. Hardcoding
# it meant the site advertised v3.0.3 while people were installing v3.0.6.
VERSION="$(git -C .. describe --tags --abbrev=0 2>/dev/null | sed 's/^v//')"
if [ -n "$VERSION" ]; then
  printf '%s' "$VERSION" > "$OUT/version.txt"
  echo "  version  $VERSION"
else
  echo "  version  skipped (no git tag) — page falls back to its built-in string"
fi

# The hero terminal prints a real, scannable QR — the same half-block art the
# CLI renders. public/qr.txt is committed, so this only regenerates when segno
# happens to be installed; the site never depends on it being present.
if python3 -c "import segno" 2>/dev/null; then
  python3 genqr.py "https://reminal.app/" public/qr.svg
else
  echo "  qr.svg   skipped (no segno) — using the committed copy"
fi

echo
echo "assets → $OUT ($(du -sh "$OUT" | cut -f1) total)"
