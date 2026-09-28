#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar
#
# Each agent started the way people start them — permissions off, a model
# picked, a prompt on the command line — then a reboot. It must come back
# with those flags (and their values), and without its prompt sent again.
# cursor-agent needs a Cursor login in the box (see run.sh); without one it
# is skipped. The box is not recreated here, so a login survives.
. "$(dirname "$0")/lib.sh"
fails=0
ok()   { echo "ok   $*"; }
fail() { echo "FAIL $*"; fails=$((fails+1)); }
skip() { echo "skip $*"; }
prompts() { bx "grep -c 'hello first' $LLMLOG 2>/dev/null || echo 0"; }
conv() { bx "cat $H/.reminal/restore/$1.conv 2>/dev/null"; }

AGENTS="claude codex gemini qwen opencode pi"
for x in ${SKIP_AGENTS:-}; do AGENTS=$(echo " $AGENTS " | sed "s/ $x / /" | tr -s ' '); skip "$x: cannot run on this box"; done
has() { echo " $AGENTS " | grep -q " $1 "; }
CURSOR=1
bxl 'cursor-agent status 2>&1' | grep -q "Logged in" || CURSOR=0

# name | command it is started with | what is typed after, if anything
start() {
    id=$(newsess f$1 $H/f-$1)
    eval "ID_$1=$id"
    send $id "$2"
}
start claude   'claude --dangerously-skip-permissions --model claude-opus-4-1 "hello first"'
start codex    "codex --dangerously-bypass-approvals-and-sandbox -m fake-model -c model_reasoning_effort=high ${CODEX_EXTRA:-} \"hello first\""
start gemini   'gemini --yolo -m fake-model -i "hello first"'
start qwen     'qwen --approval-mode yolo -m fake-model -i "hello first"'
has opencode && start opencode 'opencode -m fake/fake-model --agent build'
start pi       'pi --provider fake --model fake-1'
[ $CURSOR = 1 ] && start cursor 'cursor-agent -f "What is 1100 plus 11? Reply with only the number."'
sleep 20
has opencode && send $ID_opencode "hello first"; send $ID_pi "hello first"
[ $CURSOR = 1 ] && screen $ID_cursor | grep -q "Workspace Trust" && send $ID_cursor "a"
sleep 25
for n in $AGENTS; do
    id=$(eval echo \$ID_$n)
    screen $id | grep -q "noted: hello first" && ok "$n ($id) started with its flags, answered its first prompt" || fail "$n: did not answer its first prompt"
done
[ $CURSOR = 1 ] && { screen $ID_cursor | grep -q "1111" && ok "cursor ($ID_cursor) started with -f, answered 1111" || fail "cursor: did not answer"; } || skip "cursor: no Cursor login in the box"
sleep 17
before=$(prompts)
C_claude=$(conv $ID_claude) C_qwen=$(conv $ID_qwen)

reboot_box || exit 1
ok "rebooted"
want=$(echo $AGENTS | wc -w | tr -d ' '); [ $CURSOR = 1 ] && want=$((want+1))
for i in $(seq 1 60); do
    [ "$(nsessions)" -ge $want ] && break; sleep 2
done
sleep 30
expect() { # name expected-command-line
    came_back_as "$(eval echo \$ID_$1)" "$2" && ok "$1 came back as: $2" || fail "$1: expected '$2'"
}
expect claude   "claude --dangerously-skip-permissions --model claude-opus-4-1 --resume $C_claude"
expect codex    "codex resume --dangerously-bypass-approvals-and-sandbox -m fake-model -c model_reasoning_effort=high ${CODEX_EXTRA:+$CODEX_EXTRA }--last"
expect gemini   "gemini --yolo -m fake-model --resume latest"
expect qwen     "qwen --approval-mode yolo -m fake-model --resume $C_qwen"
has opencode && expect opencode "opencode -m fake/fake-model --agent build --continue"
if [ $CURSOR = 1 ]; then
    if [ "$T" = win ]; then expect cursor "cursor-agent -f --continue"; else expect cursor "index.js -f --continue"; fi
fi
# pi rewrites its process title: its flags cannot be seen, so none come back.
since_restore $ID_pi | tr '\n' ' ' | grep -q "pi --continue" && ok "pi came back as: pi --continue (its flags are not visible to carry)" || fail "pi: not resumed"
after=$(prompts)
[ "$after" = "$before" ] && ok "no first prompt was sent again ($before before, $after after)" || fail "a first prompt was sent again ($before → $after)"
for n in $AGENTS; do send $(eval echo \$ID_$n) "after the reboot"; done
[ $CURSOR = 1 ] && send $ID_cursor "What is 2200 plus 22? Reply with only the number."
sleep 25
for n in $AGENTS; do
    since_restore $(eval echo \$ID_$n) | grep -q "noted: after the reboot" && ok "$n answers after the restore" || fail "$n: no answer after the restore"
done
[ $CURSOR = 1 ] && { since_restore $ID_cursor | grep -q "2222" && ok "cursor answers after the restore (2222)" || fail "cursor: no answer after the restore"; }
[ $fails -eq 0 ] && echo "all passed" || { echo "$fails failed"; exit 1; }
