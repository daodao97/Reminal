#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar
#
# Two `cursor-agent -f` sessions in one folder, ended by one reboot: neither
# may be resumed by guess. Each must open cursor's own chat list — with -f
# still on. Needs a Cursor login in the box: cursor's model is real, so the
# questions have one right answer.
BOX=reminal-rh-box
fails=0
ok()   { echo "ok   $*"; }
fail() { echo "FAIL $*"; fails=$((fails+1)); }
send() { docker exec $BOX sendb.sh "$1" "$(printf '%s' "$2" | base64)" >/dev/null; }
screen() { docker exec $BOX mcp.sh read "$1" | python3 -c 'import sys,json
d=json.loads(sys.stdin.read())
for c in d.get("result",{}).get("content",[]):
    t=c.get("text","")
    if t.startswith("{"): print(json.loads(t).get("text",""))'; }
since_restore() { screen "$1" | awk '/restored this session/{b=""} {b=b"\n"$0} END{print b}'; }
docker exec $BOX bash -lc 'cursor-agent status 2>&1' | grep -q "Logged in" || { echo "skip: no Cursor login in the box"; exit 0; }
new() { docker exec $BOX bash -lc "mkdir -p /root/cs && cd /root/cs && reminal new $1 2>&1" | sed -n 's/.*background session · v[^ ]* · \([A-Z0-9]*\).*/\1/p'; }
A=$(new csA); B=$(new csB)
send $A "cursor-agent -f"; sleep 10
screen $A | grep -q "Workspace Trust" && { send $A "a"; sleep 3; }
send $B "cursor-agent -f"; sleep 10
screen $B | grep -q "Workspace Trust" && { send $B "a"; sleep 3; }
send $A "What is 9000 plus 9? Reply with only the number."
send $B "What is 9100 plus 1? Reply with only the number."
sleep 25
screen $A | grep -q "9009" && ok "csA ($A) answered 9009" || fail "csA did not answer"
screen $B | grep -q "9101" && ok "csB ($B) answered 9101" || fail "csB did not answer"
sleep 17
docker restart $BOX >/dev/null; ok "rebooted"
for i in $(seq 1 60); do docker exec $BOX reminal list 2>/dev/null | grep -q "$B" && docker exec $BOX reminal list 2>/dev/null | grep -q "$A" && break; sleep 1; done
sleep 25
n=$(docker exec $BOX sh -c 'for p in $(pgrep -f index.js); do [ "$(readlink /proc/$p/cwd)" = /root/cs ] && tr "\0" " " < /proc/$p/cmdline && echo; done' | grep -c "index.js -f --resume")
[ "$n" = 2 ] && ok "both came back as: cursor-agent -f --resume (its chat list, -f kept)" || fail "expected 2 × 'index.js -f --resume', found $n"
for s in $A $B; do
    f=$(since_restore $s | tr '\n' ' ' | tr -s ' ')
    echo "$f" | grep -q "pick this session's" && ok "$s: told to pick" || fail "$s: no pick note"
    echo "$f" | grep -qE "9009|9101" && fail "$s: a conversation was resumed by guess" || ok "$s: nothing resumed by guess"
done
[ $fails -eq 0 ] && echo "all passed" || { echo "$fails failed"; exit 1; }
