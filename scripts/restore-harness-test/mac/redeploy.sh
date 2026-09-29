#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar
# Rebuild reminal and refresh the rig's helpers in the macOS VM, then restart
# the boot daemon so it runs the new build.
DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
RIGDIR=$(CDPATH= cd -- "$DIR/.." && pwd)
ROOT=$(CDPATH= cd -- "$RIGDIR/../.." && pwd)
VM=${VM:-macOS Explore} H=/Users/harshal/rtest
put() { prlctl exec "$VM" "cat > '$2'" < "$1"; }
( cd "$ROOT" && GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o /tmp/reminal-darwin.$$ ./cmd/reminal ) || exit 1
prlctl exec "$VM" "launchctl bootout system/com.reminal.restore-test 2>/dev/null; for p in \$(pgrep -f '$H/bin/reminal'); do kill \$p; done; sleep 1"
put /tmp/reminal-darwin.$$ $H/bin/reminal; rm -f /tmp/reminal-darwin.$$
for f in mcp.sh sendb.sh rig-procs.sh; do put "$RIGDIR/$f" $H/bin/$f; done
put "$RIGDIR/fake-llm.mjs" $H/rig/fake-llm.mjs
put "$RIGDIR/setup.sh" $H/rig/setup.sh
prlctl exec "$VM" "chown -R harshal $H/bin $H/rig; chmod 755 $H/bin/*; rm -rf $H/.reminal/restore; launchctl bootstrap system /Library/LaunchDaemons/com.reminal.restore-test.plist && echo daemon-restarted"
