#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar
#
# Several conversations with the same agent in the same folder, all ended by
# one reboot: each session must come back on ITS conversation — not all on
# whichever was saved last. Every session is told its own number; after the
# restore it must show that number and no other session's.
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

# agent:how many sessions of it share /root/shared-<agent>
PLAN=${PLAN:-"claude:3 codex:2 gemini:2 qwen:2 opencode:2 pi:2"}
ALL=""
for p in $PLAN; do
    a=${p%%:*} n=${p#*:}
    docker exec $BOX mkdir -p /root/shared-$a
    i=1
    while [ $i -le $n ]; do
        id=$(docker exec $BOX bash -lc "cd /root/shared-$a && reminal new $a$i 2>&1" | sed -n 's/.*background session · v[^ ]* · \([A-Z0-9]*\).*/\1/p')
        eval "S_$a$i=$id"; ALL="$ALL $a$i"
        send $id "$a"
        i=$((i+1))
    done
done
sleep 15
num=100
for s in $ALL; do
    num=$((num+1)); eval "N_$s=$num"
    send $(eval echo \$S_$s) "my number is $num"
    sleep 3   # one after another, so "latest" is well defined
done
sleep 20
for s in $ALL; do
    id=$(eval echo \$S_$s) n=$(eval echo \$N_$s)
    screen $id | grep -q "noted: my number is $n" && ok "$s ($id) holds conversation #$n" || fail "$s: no answer before the reboot"
done
sleep 16
docker restart $BOX >/dev/null
ok "rebooted"
want=$(echo $ALL | wc -w | tr -d ' ')
for i in $(seq 1 60); do
    [ "$(docker exec $BOX sh -c 'reminal list 2>/dev/null | grep -c "[A-Z0-9]\{8\}"')" -ge "$want" ] && break; sleep 1
done
sleep 25
for s in $ALL; do
    id=$(eval echo \$S_$s) n=$(eval echo \$N_$s)
    got=$(since_restore $id | grep -o "my number is [0-9]*" | sort -u | tr '\n' ' ')
    case "$got" in
        "my number is $n ") ok "$s came back on its own conversation (#$n)" ;;
        "") fail "$s: no conversation shown after restore" ;;
        *) fail "$s: expected #$n, shows: $got" ;;
    esac
done
[ $fails -eq 0 ] && echo "all passed" || { echo "$fails failed"; exit 1; }
