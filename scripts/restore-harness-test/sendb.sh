#!/bin/sh
# Types base64-encoded text into a session (spaces and quotes survive).
exec mcp.sh send "$1" "$(echo "$2" | base64 -d)"
