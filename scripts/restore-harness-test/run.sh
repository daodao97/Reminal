#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar
#
# `reminal restore` with the real coding agents: claude, codex, gemini, qwen,
# opencode, pi (and cursor-agent, which needs a Cursor login), each holding a
# conversation with fake-llm.mjs, across a "reboot" of their box.
#
#   scripts/restore-harness-test/run.sh          # build, start, run check.sh
#   scripts/restore-harness-test/run.sh up       # build and start only
#   scripts/restore-harness-test/run.sh down     # remove it all
set -e
DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$DIR/../.." && pwd)
NET=reminal-rh-net IMG=reminal-rh-test BOX=reminal-rh-box RELAY=reminal-rh-relay VOL=reminal-rh-home

down() {
    for c in $BOX $RELAY; do
        if docker container inspect "$c" >/dev/null 2>&1; then docker rm -f "$c" >/dev/null; fi
    done
    if docker volume inspect $VOL >/dev/null 2>&1; then docker volume rm $VOL >/dev/null; fi
    if docker network inspect "$NET" >/dev/null 2>&1; then docker network rm "$NET" >/dev/null; fi
}
if [ "$1" = down ]; then down; echo stopped; exit 0; fi
if ! docker info >/dev/null 2>&1; then echo "Docker isn't running." >&2; exit 1; fi

docker build -q -t reminal-harnesses -f "$DIR/harnesses.Dockerfile" "$DIR" >/dev/null
OUT=$(mktemp -d)
trap 'rm -rf "$OUT"' EXIT
cp "$DIR/Dockerfile" "$DIR/mcp.sh" "$DIR/sendb.sh" "$DIR/fake-llm.mjs" "$DIR/setup.sh" "$ROOT/scripts/pi-test/fake-provider.ts" "$OUT/"
docker run --rm -v "$ROOT":/src:ro -v "$OUT":/out \
    -v reminal-restore-gocache:/root/.cache/go-build -v reminal-restore-gomod:/go/pkg/mod \
    -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=0 -w /src \
    golang:1.26-bookworm go build -o /out/reminal ./cmd/reminal
docker build -q -t "$IMG" "$OUT" >/dev/null

down
docker network create "$NET" >/dev/null
docker volume create $VOL >/dev/null
docker run -d --name $RELAY --network "$NET" "$IMG" reminal relay 8080 >/dev/null
docker run -d --name $BOX --init --network "$NET" -v $VOL:/root \
    -e REMINAL_RELAY=ws://$RELAY:8080/ws -e REMINAL_WEB=http://$RELAY:8080 "$IMG" >/dev/null
until docker exec $BOX test -f /root/.setup-done 2>/dev/null; do sleep 1; done
[ "$1" = up ] && { echo "up: docker exec -it $BOX bash"; exit 0; }
exec "$DIR/check.sh"
