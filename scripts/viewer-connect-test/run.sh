#!/usr/bin/env bash
# Drives the real viewer, served by a real relay, against two real sessions in
# a container — the three things that made a connect hang or hand out a PIN
# that cannot work:
#
#   1. one kex_init per connect, not two (two spent the agent's PIN allowance
#      twice per connect, and it refills one per 10s)
#   2. a wrong PIN keeps saying "PIN mismatch" instead of being overwritten by
#      its own watchdog with "Handshake timed out" nine seconds later
#   3. switching sessions does not carry the previous session's PIN, into the
#      handshake or into what Share hands out
#
# Needs docker and playwright. Nothing here touches the host's reminal.
set -euo pipefail
cd "$(dirname "$0")"
ROOT=$(cd ../.. && pwd)
PORT=${PORT:-18080}
NAME=reminal-viewer-test

GOOS=linux GOARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/') \
  go build -C "$ROOT" -o "$PWD/reminal" ./cmd/reminal
docker build -q -t "$NAME" . >/dev/null
docker rm -f "$NAME" >/dev/null 2>&1 || true
docker run -d --name "$NAME" -p "$PORT:18080" "$NAME" >/dev/null
trap 'docker rm -f "$NAME" >/dev/null 2>&1 || true; rm -f "$PWD/reminal"' EXIT
docker exec -d "$NAME" sh -c 'reminal relay 18080 >/tmp/relay.log 2>&1'
sleep 3
docker exec "$NAME" sh -c 'cd /root && reminal new alpha >/dev/null 2>&1 && reminal new beta >/dev/null 2>&1'
creds=$(docker exec "$NAME" sh -c 'for s in alpha beta; do reminal info $s | grep -E "Session:|PIN:" | awk "{print \$2}"; done' | tr '\n' ' ')
echo "sessions: $creds"
node checks.mjs $creds
