#!/bin/sh
# Types base64-encoded text into a session (spaces and quotes survive).
# SLOW_ENTER=1: the text, a second, then Enter on its own — an agent on a slow
# box (qwen under Windows ARM emulation) may not be ready for the Enter
# reminal sends 250ms after the text.
keys=$(printf '%s' "$2" | base64 --decode)
if [ -n "${SLOW_ENTER:-}" ]; then
    mcp.sh type "$1" "$keys" >/dev/null; sleep 1; exec mcp.sh send "$1" ""
fi
exec mcp.sh send "$1" "$keys"
