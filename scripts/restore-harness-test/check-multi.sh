#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar
#
# Several conversations with the same agent in the same folder, all ended by
# one reboot — where "the latest conversation here" would be the same one for
# all of them. Each session is told its own number. After the restore:
#   by id   (claude, qwen — their hooks report it): each is back on ITS
#           conversation, showing its number and no other session's;
#   picker  (codex; a claude that never got to report an id): the agent's own
#           list is open to choose from, nothing resumed by guess;
#   neither (gemini, opencode, pi): the agent is not started, and a line
#           says how to find the conversation.
# Run in small batches — every session is a real agent: PLAN="claude:3 qwen:2".
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
cmd_of() { case $1 in cursor) echo cursor-agent ;; *) echo $1 ;; esac; }

PLAN=${PLAN:-"claude:3 qwen:2"}
NOID=${NOID:-}          # an agent to add one more session of, never spoken to
ALL=""
for p in $PLAN; do
    a=${p%%:*} n=${p#*:}
    docker exec $BOX mkdir -p /root/shared-$a
    i=1
    while [ $i -le $n ]; do
        id=$(docker exec $BOX bash -lc "cd /root/shared-$a && reminal new $a$i 2>&1" | sed -n 's/.*background session · v[^ ]* · \([A-Z0-9]*\).*/\1/p')
        eval "S_$a$i=$id A_$a$i=$a"; ALL="$ALL $a$i"
        send $id "$(cmd_of $a)"
        i=$((i+1))
    done
done
if [ -n "$NOID" ]; then
    id=$(docker exec $BOX bash -lc "cd /root/shared-$NOID && reminal new ${NOID}silent 2>&1" | sed -n 's/.*background session · v[^ ]* · \([A-Z0-9]*\).*/\1/p')
    eval "S_${NOID}silent=$id A_${NOID}silent=$NOID"
    send $id "$(cmd_of $NOID)"
fi
sleep 15
num=100
for s in $ALL; do
    num=$((num+1)); eval "N_$s=$num"
    send $(eval echo \$S_$s) "my number is $num"
    sleep 3
done
sleep 12
for s in $ALL; do
    id=$(eval echo \$S_$s) n=$(eval echo \$N_$s)
    screen $id | grep -q "noted: my number is $n" && ok "$s ($id) holds conversation #$n" || fail "$s: no answer before the reboot"
done
sleep 16
docker restart $BOX >/dev/null
ok "rebooted"
want=$(echo $ALL ${NOID:+x} | wc -w | tr -d ' ')
for i in $(seq 1 60); do
    [ "$(docker exec $BOX sh -c 'reminal list 2>/dev/null | grep -c "[A-Z0-9]\{8\}"')" -ge "$want" ] && break; sleep 1
done
sleep 25
procs=$(docker exec $BOX ps -eo args)

check_session() { # name agent id number
    # Joined into one line: the terminal wraps a note where it likes.
    s=$1 a=$2 id=$3 n=$4 out=$(since_restore $3) flat=$(since_restore $3 | tr '\n' ' ' | tr -s ' ')
    case $a in
    claude|qwen)
        if [ -z "$n" ]; then    # never spoke: nothing to resume by id
            echo "$flat" | grep -q "pick this session's" && ok "$s: never reported an id — told to pick" || fail "$s: no pick note"
            echo "$procs" | grep -qE "^(node /usr/local/bin/)?$a --resume\$" && ok "$s: $a's own list opened ($a --resume)" || fail "$s: picker not opened"
            echo "$out" | grep -q "noted: my number" && fail "$s: resumed someone's conversation" || ok "$s: no one's conversation resumed by guess"
            return
        fi
        got=$(echo "$out" | grep -o "my number is [0-9]*" | sort -u | tr '\n' ' ')
        [ "$got" = "my number is $n " ] && ok "$s: back on its own conversation (#$n) — none of the others" || fail "$s: expected #$n, shows: '$got'" ;;
    codex|cursor)
        echo "$flat" | grep -q "pick this session's" && ok "$s: told to pick" || fail "$s: no pick note"
        echo "$procs" | grep -qE "codex resume\$" && ok "$s: codex's own list opened (codex resume)" || fail "$s: picker not opened"
        echo "$out" | grep -q "noted: my number" && fail "$s: resumed a conversation by guess" || ok "$s: nothing resumed by guess" ;;
    *)
        echo "$flat" | grep -q "to find this one's conversation" && ok "$s: not started; the note says how to find it" || fail "$s: no note"
        echo "$out" | grep -q "noted: my number" && fail "$s: resumed a conversation by guess" || ok "$s: nothing resumed by guess" ;;
    esac
}
for s in $ALL ${NOID:+${NOID}silent}; do
    check_session $s $(eval echo \$A_$s) $(eval echo \$S_$s) "$(eval echo \${N_$s:-})"
done
case "$PLAN" in *gemini*|*opencode*|*pi:*)
    for a in gemini opencode pi; do
        case "$PLAN" in *$a:*) echo "$procs" | grep -qE "(^| |/)$a( |\$)" && fail "$a: started although it could not know whose conversation" || ok "$a: not started in any of its sessions" ;; esac
    done ;;
esac
[ $fails -eq 0 ] && echo "all passed" || { echo "$fails failed"; for s in $ALL; do echo "----- $s"; since_restore $(eval echo \$S_$s) | tail -8; done; exit 1; }
