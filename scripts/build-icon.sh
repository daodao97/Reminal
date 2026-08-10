#!/usr/bin/env bash
# Build assets/reminal.icns — the "re" app-mark (matches the web viewer favicon:
# rounded rect, bg #0d1117, "re" in Menlo-Bold #58a6ff). macOS-only (needs
# swiftc + iconutil). Run from the repo root: scripts/build-icon.sh
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

swiftc "$here/icon/reminal-icon.swift" -o "$work/mkicon"

iconset="$work/reminal.iconset"
mkdir -p "$iconset"
"$work/mkicon" "$iconset"

mkdir -p "$root/assets"
iconutil -c icns "$iconset" -o "$root/assets/reminal.icns"
cp "$iconset/icon_512x512@2x.png" "$root/assets/reminal-icon-1024.png"

echo "built: assets/reminal.icns ($(du -h "$root/assets/reminal.icns" | cut -f1))"
