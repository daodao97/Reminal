#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar
#
# Every real coding agent, mid-conversation, across a reboot of its box. For
# each: it talks to fake-llm before; after the reboot the daemon brings its
# session back with the same id, the agent is started again with the right
# resume command — by conversation id where its hook reported one — and it
# shows the earlier conversation and answers. RIG_TARGET picks the box.
. "$(dirname "$0")/lib.sh"
fails=0
ok()   { echo "ok   $*"; }
fail() { echo "FAIL $*"; fails=$((fails+1)); }
skip() { echo "skip $*"; }
record() { bx "node -e \"const r=require('$H/.reminal/restore/$1.json'); console.log('$2'==='conv' ? (r.conv||'') : (r.fg||''))\""; }
setid() { eval "ID_$1=$2"; }
id()    { eval "echo \$ID_$1"; }

HARNESSES=${HARNESSES:-"claude:claude qwen:qwen gemini:gemini codex:codex opencode:opencode pi:pi cursor:cursor-agent"}
for s in ${SKIP_AGENTS:-}; do skip "$s: cannot run on this box"; HARNESSES=$(echo "$HARNESSES" | tr ' ' '\n' | grep -v "^$s:" | tr '\n' ' '); done
CURSOR=0; bxl 'cursor-agent status 2>&1' | grep -q "Logged in" && CURSOR=1
for h in $HARNESSES; do
    name=${h%%:*} cmd=${h#*:}
    setid $name "$(newsess $name $H/p-$name)"
    [ $name = codex ] && cmd="codex ${CODEX_EXTRA:-}"
    send $(id $name) "$cmd"
done
sleep 15
[ $CURSOR = 1 ] && screen $(id cursor) | grep -q "Workspace Trust" && { send $(id cursor) "a"; sleep 3; }
for h in $HARNESSES; do
    n=${h%%:*}
    if [ $n = cursor ] && [ $CURSOR = 1 ]; then send $(id $n) "What is 4200 plus 42? Reply with only the number."
    else send $(id $n) "remember the number 4242"; fi
done
sleep 20
for h in $HARNESSES; do
    n=${h%%:*}; sid=$(id $n)
    if [ $n = cursor ]; then
        if [ $CURSOR = 1 ]; then
            screen $sid | grep -q "4242" && ok "cursor ($sid) is in a conversation" || fail "cursor: no answer before the reboot"
        else
            screen $sid | grep -q "log in" && skip "cursor: needs a Cursor login — conversation not testable here" || fail "cursor: unexpected screen"
        fi
        continue
    fi
    screen $sid | grep -q "noted: remember the number 4242" && ok "$n ($sid) is in a conversation" || fail "$n: no answer before the reboot"
done
sleep 17   # one restore save
for h in $HARNESSES; do
    n=${h%%:*}; fg=$(record $(id $n) fg); want=${h#*:}
    [ "$fg" = "$want" ] && ok "$n: saved as running $fg" || fail "$n: saved as running '$fg'"
done
for n in claude qwen gemini; do
    c=$(record $(id $n) conv); eval "CONV_$n=$c"; [ -n "$c" ] && ok "$n: its hook reported conversation $c" || fail "$n: no conversation id"
done

reboot_box || exit 1
ok "box rebooted with all of them mid-conversation"
want=$(echo $HARNESSES | wc -w | tr -d ' ')
for i in $(seq 1 90); do [ "$(nsessions)" -ge $want ] && break; sleep 2; done
sleep 25
list=$(bx 'reminal list 2>/dev/null')
resume_of() {
    case $1 in
        claude) echo "claude --resume $CONV_claude" ;;
        qwen) echo "qwen --resume $CONV_qwen" ;;
        gemini) echo "gemini --resume latest" ;;
        codex) echo "codex resume ${CODEX_EXTRA:+$CODEX_EXTRA }--last" ;;
        opencode) echo "opencode --continue" ;;
        cursor) echo "cursor-agent --continue" ;;
    esac
}
for h in $HARNESSES; do
    n=${h%%:*}
    if [ $n = cursor ] && [ $CURSOR = 1 ]; then send $(id $n) "What is 2200 plus 22? Reply with only the number."
    else send $(id $n) "what number"; fi
done
sleep 20
for h in $HARNESSES; do
    n=${h%%:*}; sid=$(id $n)
    echo "$list" | grep -q "$sid" && ok "$n: back as $sid" || { fail "$n: $sid not back"; continue; }
    want=$(resume_of $n)
    s=$(since_restore $sid)
    if [ $n = cursor ]; then
        echo "$s" | grep -q "cursor-agent --continue" && ok "$n: typed cursor-agent --continue" || fail "$n: resume not typed"
        [ $CURSOR = 1 ] || continue
        echo "$s" | grep -q "4242" && ok "$n: shows the conversation from before the reboot" || fail "$n: earlier conversation not shown"
        echo "$s" | grep -q "2222" && ok "$n: answers in it" || fail "$n: does not answer after restore"
        continue
    fi
    if [ -n "$want" ]; then
        came_back_as $sid "$want" && ok "$n: started again as: $want" || fail "$n: not started as '$want'"
    fi
    echo "$s" | grep -q "remember the number 4242" && ok "$n: shows the conversation from before the reboot" || fail "$n: earlier conversation not shown"
    echo "$s" | grep -q "noted: what number" && ok "$n: answers in it" || fail "$n: does not answer after restore"
done

[ $fails -eq 0 ] && echo "all passed" || { echo "$fails failed"; exit 1; }
