#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar
#
# Every real coding agent, mid-conversation, across a reboot of its box
# (run.sh starts it). For each: it talks to fake-llm before; after the
# reboot the daemon brings its session back with the same id, the agent is
# started again with the right resume command — by conversation id where its
# hook reported one — and it shows the earlier conversation and answers.
BOX=reminal-rh-box
fails=0
ok()   { echo "ok   $*"; }
fail() { echo "FAIL $*"; fails=$((fails+1)); }
skip() { echo "skip $*"; }
send() { docker exec $BOX sendb.sh "$1" "$(printf '%s' "$2" | base64)" >/dev/null; }
screen() { docker exec $BOX mcp.sh read "$1" | python3 -c 'import sys,json
d=json.loads(sys.stdin.read())
for c in d.get("result",{}).get("content",[]):
    t=c.get("text","")
    if t.startswith("{"): print(json.loads(t).get("text",""))'; }
since_restore() { screen "$1" | awk '/restored this session/{b=""} {b=b"\n"$0} END{print b}'; }
record() { docker exec $BOX node -e "const r=require('/root/.reminal/restore/$1.json'); console.log(process.argv[1]==='conv' ? (r.conv||'') : (r.fg||''))" "$2"; }

# harness | how it is started | needs an account the rig does not have
HARNESSES="claude:claude qwen:qwen gemini:gemini codex:codex opencode:opencode pi:pi cursor:cursor-agent"
# (plain variables: macOS ships bash 3.2, which has no associative arrays)
setid() { eval "ID_$1=$2"; }
id()    { eval "echo \$ID_$1"; }
for h in $HARNESSES; do
    name=${h%%:*} cmd=${h#*:}
    setid $name $(docker exec $BOX bash -lc "cd /root/p-$name && reminal new $name 2>&1" | sed -n 's/.*background session · v[^ ]* · \([A-Z0-9]*\).*/\1/p')
    send $(id $name) "$cmd"
done
sleep 15
for h in $HARNESSES; do send $(id ${h%%:*}) "remember the number 4242"; done
sleep 12
for h in $HARNESSES; do
    n=${h%%:*}; id=$(id $n)
    if [ $n = cursor ]; then
        screen $id | grep -q "log in" && skip "$n: needs a Cursor login — conversation not testable here" || fail "$n: unexpected screen"
        continue
    fi
    screen $id | grep -q "noted: remember the number 4242" && ok "$n ($id) is in a conversation" || fail "$n: no answer before the reboot"
done
sleep 17   # one restore save
for h in $HARNESSES; do
    n=${h%%:*}; fg=$(record $(id $n) fg); want=${h#*:}
    [ "$fg" = "$want" ] && ok "$n: saved as running $fg" || fail "$n: saved as running '$fg'"
done
for n in claude qwen gemini; do
    c=$(record $(id $n) conv); eval "CONV_$n=$c"; [ -n "$c" ] && ok "$n: its hook reported conversation $c" || fail "$n: no conversation id"
done

docker restart $BOX >/dev/null
ok "box rebooted with all of them mid-conversation"
for i in $(seq 1 60); do
    [ "$(docker exec $BOX sh -c 'reminal list 2>/dev/null | grep -c "[A-Z0-9]\{8\}"')" = 7 ] && break; sleep 1
done
sleep 20
list=$(docker exec $BOX reminal list 2>/dev/null)
procs=$(docker exec $BOX ps -eo args)
resume_of() {
    case $1 in
        claude) echo "claude --resume $CONV_claude" ;;
        qwen) echo "qwen --resume $CONV_qwen" ;;
        gemini) echo "gemini --resume latest" ;;
        codex) echo "codex resume --last" ;;
        opencode) echo "opencode --continue" ;;
        cursor) echo "cursor-agent --continue" ;;
    esac
}
for h in $HARNESSES; do send $(id ${h%%:*}) "what number"; done
sleep 12
for h in $HARNESSES; do
    n=${h%%:*}; id=$(id $n)
    echo "$list" | grep -q "$id" && ok "$n: back as $id" || { fail "$n: $id not back"; continue; }
    want=$(resume_of $n)
    if [ -n "$want" ]; then
        if [ $n = cursor ]; then
            since_restore $id | grep -q "cursor-agent --continue" && ok "$n: typed cursor-agent --continue" || fail "$n: resume not typed"
            continue
        fi
        echo "$procs" | grep -q -- "$want" && ok "$n: started again as: $want" || fail "$n: not started as '$want'"
    fi
    s=$(since_restore $id)
    echo "$s" | grep -q "remember the number 4242" && ok "$n: shows the conversation from before the reboot" || fail "$n: earlier conversation not shown"
    echo "$s" | grep -q "noted: what number" && ok "$n: answers in it" || fail "$n: does not answer after restore"
done

[ $fails -eq 0 ] && echo "all passed" || { echo "$fails failed"; exit 1; }
