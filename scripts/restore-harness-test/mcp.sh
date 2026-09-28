#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar
#
# mcp.sh send <id> <keys> | mcp.sh read <id> — one reminal MCP call, the
# JSON built by node so any text (quotes, backslashes) is typed as given.
op=$1 id=$2 keys=$3
call=$(OP="$op" ID="$id" KEYS="$keys" node -e '
const {OP, ID, KEYS} = process.env;
const args = OP === "send" ? {session: ID, keys: KEYS, enter: true} : {session: ID};
console.log(JSON.stringify({jsonrpc: "2.0", id: 2, method: "tools/call",
  params: {name: OP === "send" ? "send_keys" : "read_transcript", arguments: args}}));')
init='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}'
( printf '%s\n%s\n' "$init" "$call"; sleep 2 ) | reminal mcp 2>/dev/null | tail -1
