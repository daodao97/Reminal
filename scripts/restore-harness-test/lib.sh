# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar
#
# Where the restore checks run, and how they reach it. RIG_TARGET picks:
#   docker (default) — the reminal-rh-box container run.sh starts;
#   mac              — the "macOS Explore" Parallels VM, set up by mac/setup-vm.sh,
#                      everything as its user under a throwaway home.
# Every check talks to its box only through these, so each one is the same
# test wherever it runs.
T=${RIG_TARGET:-docker}
case $T in
docker)
    BOX=${BOX:-reminal-rh-box} H=/root LLMLOG=/tmp/fake-llm.log
    bx()  { docker exec $BOX sh -c "$1"; }
    bxl() { docker exec $BOX bash -lc "$1"; }
    reboot_box() { docker restart $BOX >/dev/null; }
    ;;
mac)
    VM=${VM:-macOS Explore} H=/Users/harshal/rtest LLMLOG=/Users/harshal/rtest/fake-llm.log
    # A command crosses as base64: prlctl splits what it is given on spaces.
    bx()  { prlctl exec "$VM" "$H/runb64" "$(printf '%s' "$1" | base64)"; }
    bxl() { bx "$1"; }
    # A real reboot, the orderly kind: every process gets SIGTERM.
    reboot_box() {
        old=$(bx 'sysctl -n kern.boottime' 2>/dev/null)
        prlctl exec "$VM" 'shutdown -r now' >/dev/null 2>&1
        for i in $(seq 1 120); do
            sleep 5
            now=$(bx 'sysctl -n kern.boottime' 2>/dev/null) || continue
            [ -n "$now" ] && [ "$now" != "$old" ] && return 0
        done
        echo "FAIL the VM did not come back from its reboot"; return 1
    }
    ;;
win)
    VM=${VM:-Windows 11} H=C:/rtest/home LLMLOG=C:/rtest/home/fake-llm.log
    # A command crosses as base64 into Git Bash in the rig's world (win/env.ps1).
    bx()  { prlctl exec "$VM" powershell -NoProfile -ExecutionPolicy Bypass -File 'C:\rtest\runb64.ps1' "$(printf '%s' "$1" | base64)" | tr -d '\r'; }
    bxl() { bx "$1"; }
    # Sessions get what a person's would: PowerShell, not the Git Bash the
    # checks run in (reminal honours $SHELL on Windows).
    NEWENV="SHELL="
    # Run as SYSTEM, codex will not start its background server ("start the
    # Windows daemon from a non-elevated terminal"); --no-daemon is its own way
    # round that, and its resume must carry it.
    CODEX_EXTRA="--no-daemon"
    SLOWENTER=1
    # opencode's Windows ARM64 build cannot start its TUI ("bun:ffi dlopen()
    # is not available in this build") — upstream, nothing reminal can change.
    SKIP_AGENTS="opencode"
    reboot_box() {
        old=$(bx 'powershell -NoProfile -c "(Get-CimInstance Win32_OperatingSystem).LastBootUpTime.Ticks"' 2>/dev/null)
        prlctl exec "$VM" shutdown /r /t 0 >/dev/null 2>&1
        for i in $(seq 1 120); do
            sleep 5
            now=$(bx 'powershell -NoProfile -c "(Get-CimInstance Win32_OperatingSystem).LastBootUpTime.Ticks"' 2>/dev/null) || continue
            [ -n "$now" ] && [ "$now" != "$old" ] && return 0
        done
        echo "FAIL the VM did not come back from its reboot"; return 1
    }
    ;;
*) echo "RIG_TARGET must be docker, mac or win" >&2; exit 2 ;;
esac

send()   { bx "${SLOWENTER:+SLOW_ENTER=1 }sendb.sh $1 $(printf '%s' "$2" | base64)" >/dev/null; }
screen() { bx "mcp.sh read $1" | python3 -c 'import sys,json
try: d=json.loads(sys.stdin.read())
except Exception: sys.exit(0)
for c in d.get("result",{}).get("content",[]):
    t=c.get("text","")
    if t.startswith("{"): print(json.loads(t).get("text",""))'; }
since_restore() { screen "$1" | awk '/restored this session/{b=""} {b=b"\n"$0} END{print b}'; }
# newsess NAME DIR — a background session started in DIR; prints its id.
newsess() { bxl "mkdir -p $2 && cd $2 && ${NEWENV:-} reminal new $1 2>&1" | sed -n 's/.*background session · v[^ ]* · \([A-Z0-9]*\).*/\1/p'; }
nsessions() { bx 'reminal list 2>/dev/null' | grep -c "[A-Z0-9]\{8\}"; }
if [ "$T" = win ]; then
    procs() { bx 'powershell -NoProfile -c "Get-CimInstance Win32_Process | % CommandLine"'; }
else
    procs() { bx 'ps -axo args'; }
fi
# typed ID TEXT — reminal typed TEXT into the restored session (a wrapped
# line may have lost the space it broke at, or kept it).
typed() {
    s=$(since_restore "$1")
    echo "$s" | tr -d '\n' | grep -qF -- "$2" || echo "$s" | tr '\n' ' ' | tr -s ' ' | grep -qF -- "$2"
}
# came_back_as ID COMMAND — the restored session runs COMMAND: by its process
# where command lines read like one (Linux, macOS); on Windows, where they
# read `"C:\...\node.exe" "...\cli.js" ...`, by what reminal typed — that it
# then runs is what the "shows the conversation / answers" checks prove.
came_back_as() {
    if [ "$T" = win ]; then typed "$1" "$2"; else procs | grep -qF -- "$2"; fi
}
# args_in DIR PATTERN — command lines of processes whose cwd is DIR.
args_in() { bx "rig-procs.sh '$1' '$2'"; }
killall_sessions() { bx 'for id in $(reminal list 2>/dev/null | grep -oE "[A-Z0-9]{8}"); do reminal kill $id -y >/dev/null; done'; }
