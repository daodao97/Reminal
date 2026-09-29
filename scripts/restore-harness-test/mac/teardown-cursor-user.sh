#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar
# Undo setup-cursor-user.sh: its boot daemon, its processes, the rigtest user and home.
VM=${VM:-macOS Explore}
prlctl exec "$VM" "launchctl bootout system/com.reminal.restore-test-cursor 2>/dev/null; rm -f /Library/LaunchDaemons/com.reminal.restore-test-cursor.plist; pkill -u rigtest; sleep 1; pkill -9 -u rigtest; sysadminctl -deleteUser rigtest >/dev/null 2>&1; rm -rf /Users/rigtest; id rigtest 2>&1 | head -1"
