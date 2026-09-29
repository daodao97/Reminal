#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar
#
# Two `cursor-agent -f` sessions in one folder, ended by one reboot: neither
# may be resumed by guess. Each must open cursor's own chat list — with -f
# still on. Needs a Cursor login in the box: cursor's model is real, so the
# questions have one right answer.
. "$(dirname "$0")/lib.sh"
fails=0
ok()   { echo "ok   $*"; }
fail() { echo "FAIL $*"; fails=$((fails+1)); }
cursor_logged_in || { echo "skip: no Cursor login in the box"; exit 0; }
new() { newsess $1 $H/cs; }
A=$(new csA); B=$(new csB)
send $A "cursor-agent -f"; ready $A cursor-agent
shows $A "Workspace Trust" && { send $A "a"; sleep 3; }
send $B "cursor-agent -f"; ready $B cursor-agent
shows $B "Workspace Trust" && { send $B "a"; sleep 3; }
send $A "What is 9000 plus 9? Reply with only the number."
send $B "What is 9100 plus 1? Reply with only the number."
eventually 90 shows $A "9009" && ok "csA ($A) answered 9009" || fail "csA did not answer"
eventually 90 shows $B "9101" && ok "csB ($B) answered 9101" || fail "csB did not answer"
eventually 40 saved $A cursor-agent; eventually 40 saved $B cursor-agent
reboot_box || exit 1; ok "rebooted"
eventually 240 enough_sessions 2
for s in $A $B; do eventually 90 shows_after $s "cursor-agent -f --resume"; done
sleep 5
if [ "$T" = win ]; then
    n=0; for s in $A $B; do typed $s "cursor-agent -f --resume" && n=$((n+1)); done
else
    n=$(args_in $H/cs index.js | grep -c "index.js -f --resume")
fi
[ "$n" = 2 ] && ok "both came back as: cursor-agent -f --resume (its chat list, -f kept)" || {
    fail "expected 2 × 'index.js -f --resume', found $n"
    for s in $A $B; do echo "  $s typed: $(since_restore $s | grep -v '^\s*$' | sed -n 2,3p | tr '\n' ' ' | cut -c1-200)"; done
}
for s in $A $B; do
    f=$(since_restore $s | tr '\n' ' ' | tr -s ' ')
    printf '%s\n' "$f" | grep -q "pick this session's" && ok "$s: told to pick" || fail "$s: no pick note"
    printf '%s\n' "$f" | grep -qE "9009|9101" && fail "$s: a conversation was resumed by guess" || ok "$s: nothing resumed by guess"
done
[ $fails -eq 0 ] && echo "all passed" || { echo "$fails failed"; exit 1; }
