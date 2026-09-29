#!/bin/sh
# rig-procs.sh DIR PATTERN — the command line of every process matching
# PATTERN whose working directory is DIR, one per line, on Linux or macOS.
for p in $(pgrep -f "$2"); do
    if [ -d /proc/$p ]; then
        [ "$(readlink /proc/$p/cwd)" = "$1" ] && tr '\0' ' ' < /proc/$p/cmdline && echo
    else
        [ "$(lsof -a -d cwd -Fn -p $p 2>/dev/null | sed -n 's/^n//p')" = "$1" ] && ps -o args= -p $p
    fi
done
