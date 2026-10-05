#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar
#
# Proves the test suite cannot touch the real ~/.reminal. In a container whose
# HOME is the real one, plant canary at-rest files, run every package's tests
# (with and without -tags orgserver), and fail if anything under ~/.reminal
# changed, appeared or vanished. Nothing on the host is touched.
#
#   scripts/atrest-canary/run.sh
set -e
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
if ! docker info >/dev/null 2>&1; then echo "Docker isn't running." >&2; exit 1; fi
docker run --rm -v "$ROOT":/src:ro \
    -v reminal-canary-gocache:/root/.cache/go-build -v reminal-canary-gomod:/go/pkg/mod \
    -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=0 -w /src \
    golang:1.26-bookworm sh -c '
set -e
H=$HOME/.reminal; mkdir -p $H/restore $H/org-rooms
printf "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n" > $H/atrest.key
printf "{\"v\":1,\"source\":\"file\",\"id\":\"canary0canary0ca\"}" > $H/atrest.json
printf "canary" > $H/restore/CANARY01.sealed
printf "canary" > $H/org-rooms/canary.json
printf "canary" > $H/device_ed25519
snapshot() { (cd $H && find . -type f | sort | xargs -r sha256sum; find . | sort | wc -l); }
snapshot > /tmp/before
echo "== go test ./... (HOME=$HOME, the real one in this container)"
go test ./... 2>&1 | grep -E "^(FAIL|ok|---)" | grep -v "^ok" || true
echo "== go test -tags orgserver ./..."
go test -tags orgserver ./... 2>&1 | grep -E "^(FAIL|---)" || true
snapshot > /tmp/after
if cmp -s /tmp/before /tmp/after; then
  echo "CANARY OK: nothing under ~/.reminal changed"
else
  echo "CANARY FAILED: the test suite touched the real ~/.reminal"; diff /tmp/before /tmp/after; exit 1
fi'
