#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar
#
# The whole restore suite against the real agents, no more than seven at once
# (each is a real node process; Docker Desktop's VM falls over well before
# twenty), each part from a clean start, each ending in a reboot:
#   1. each agent alone in its folder — resumed exactly (check.sh)
#   2-4. several of one agent in one folder (check-multi.sh)
#   5. each started with its flags and a prompt (check-flags.sh)
# RIG_TARGET=docker (default: a fresh container each part, removed at the
# end) or mac (the macOS VM set up by mac/setup-vm.sh). cursor-agent needs a
# Cursor login in the box; without one its conversation checks are skipped.
DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$DIR/lib.sh"
rc=0
part() {
    echo "===== $1"; shift
    out=$("$@" 2>&1)
    echo "$out" | grep -E "^(ok|FAIL|skip|all|[0-9]+ failed)"
    echo "$out" | grep -q "^all passed" || rc=1
}
fresh() {
    if [ "$T" = docker ]; then "$DIR/run.sh" up >/dev/null; return; fi
    killall_sessions
    sleep 3   # Windows keeps a folder busy until its last process has gone
    bx "rm -rf $H/.reminal/restore $H/shared-* $H/f-* $H/cs; : > $LLMLOG"
}
fresh
part "each agent alone in its folder" "$DIR/check.sh"
for plan in "claude:3 qwen:2|claude" "codex:2 gemini:2|" "opencode:2 pi:2|"; do
    p=${plan%%|*}
    for x in ${SKIP_AGENTS:-}; do p=$(echo " $p " | sed "s/ $x:[0-9]* / /" | tr -s ' ' | sed 's/^ //; s/ $//'); done
    fresh
    part "shared folders: $p" env PLAN="$p" NOID="${plan#*|}" "$DIR/check-multi.sh"
done
fresh
part "started with flags" "$DIR/check-flags.sh"
if [ "$T" = docker ]; then "$DIR/run.sh" down >/dev/null; else killall_sessions; fi
exit $rc
