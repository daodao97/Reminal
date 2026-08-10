#!/bin/bash
set -euo pipefail

VERSION="${VERSION:-1.12.3}"
OUTPUT="${OUTPUT:-dist/reminal}"

mkdir -p dist

RELAY_LDFLAGS="-X github.com/reminal/reminal/internal/config.DefaultCloudRelay=wss://reminal-relay.futuristic.workers.dev/ws -X github.com/reminal/reminal/internal/config.DefaultCloudWeb=https://reminal-relay.futuristic.workers.dev"
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
BUILD_LDFLAGS="-X main.buildDate=${BUILD_DATE} -X main.commit=${COMMIT}"

echo "Building reminal ${VERSION}..."
CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION} ${BUILD_LDFLAGS} ${RELAY_LDFLAGS}" -o "${OUTPUT}" ./cmd/reminal

echo "Built ${OUTPUT}"
"${OUTPUT}" version

# macOS: compile the ScreenCaptureKit capture helper alongside the binary. The
# agent auto-discovers reminal-capture next to itself and uses it for a native,
# ~30fps window mirror (falling back to screencapture if it's absent). Skipped on
# non-macOS and when swiftc isn't installed — the mirror still works, just slower.
if [[ "$(uname)" == "Darwin" ]]; then
  if command -v swiftc >/dev/null 2>&1; then
    HELPER="$(dirname "${OUTPUT}")/reminal-capture"
    echo "Building capture helper ${HELPER}..."
    # Same 12.3 deployment target as CI (the ScreenCaptureKit floor), so a
    # locally-built helper doesn't silently require the build machine's OS.
    swiftc -O -target "$(uname -m)-apple-macos12.3" -o "${HELPER}" native/reminal-capture/main.swift
    codesign --force --sign - "${HELPER}" >/dev/null 2>&1 || true
    echo "Built ${HELPER}"
    # Assemble reminal.app so local builds match the release layout (one signed
    # bundle: CLI + helper + icon). Ad-hoc signed here — an ad-hoc bundle can't
    # persist a Screen Recording grant (releases use the stable cert), but it's
    # enough to exercise the bundle path locally.
    OUT_DIR="$(dirname "${OUTPUT}")"
    if [ -f assets/reminal.icns ]; then
      VERSION="${VERSION}" "$(dirname "$0")/build-app.sh" "${OUT_DIR}" "${OUT_DIR}" - >/dev/null && echo "Assembled ${OUT_DIR}/reminal.app"
    fi
  else
    echo "swiftc not found — skipping capture helper (window mirror will use screencapture)"
  fi
fi
