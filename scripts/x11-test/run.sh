#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar
#
# Run reminal's X11 window-backend tests against a real headless desktop.
#
#   scripts/x11-test/run.sh                 # the gated TestX11* suite
#   scripts/x11-test/run.sh sh              # a shell inside the desktop
#   scripts/x11-test/run.sh go test ./...   # anything else
set -e

DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$DIR/../.." && pwd)
IMAGE=reminal-x11-test

if ! docker info >/dev/null 2>&1; then
    echo "Docker isn't running — start Docker Desktop (open -a Docker on macOS) and try again." >&2
    exit 1
fi

docker build -t "$IMAGE" "$DIR"
# The module cache is mounted so a rebuild does not re-download the world, and
# the repo is mounted read-write only because `go test` wants to write its cache.
exec docker run --rm -it \
    -v "$ROOT":/src \
    -v reminal-x11-gocache:/root/.cache/go-build \
    -v reminal-x11-gomod:/go/pkg/mod \
    "$IMAGE" "$@"
