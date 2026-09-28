#!/bin/sh
# mcp.sh send <id> <keys> | mcp.sh read <id>
init='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}'
if [ "$1" = send ]; then
  call=$(printf '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"send_keys","arguments":{"session":"%s","keys":"%s","enter":true}}}' "$2" "$3")
else
  call=$(printf '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"read_transcript","arguments":{"session":"%s"}}}' "$2")
fi
( printf '%s\n%s\n' "$init" "$call"; sleep 2 ) | reminal mcp 2>/dev/null | tail -1
