#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar
#
# The whole restore suite against the real agents, a fresh box each part and
# no more than seven agents at once (each is a real node process; Docker
# Desktop's VM falls over well before twenty):
#   1. each agent alone in its folder — resumed exactly (check.sh)
#   2-4. several of one agent in one folder (check-multi.sh)
#   5. each started with its flags and a prompt (check-flags.sh)
# cursor-agent needs a Cursor login, which a fresh box has not got: log in
# in the box (`cursor-agent login`) and run check-flags.sh and
# check-cursor-shared.sh by hand for it.
# Leaves nothing running: `run.sh down` at the end.
DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
rc=0
part() {
    echo "===== $1"; shift
    out=$("$@" 2>&1)
    echo "$out" | grep -E "^(ok|FAIL|skip|all|[0-9]+ failed)"
    echo "$out" | grep -q "^all passed" || rc=1
}
part "each agent alone in its folder" "$DIR/run.sh"
for plan in "claude:3 qwen:2|claude" "codex:2 gemini:2|" "opencode:2 pi:2|"; do
    "$DIR/run.sh" up >/dev/null
    part "shared folders: ${plan%%|*}" env PLAN="${plan%%|*}" NOID="${plan#*|}" "$DIR/check-multi.sh"
done
"$DIR/run.sh" up >/dev/null
part "started with flags" "$DIR/check-flags.sh"
"$DIR/run.sh" down >/dev/null
exit $rc
