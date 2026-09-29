#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar
# Undo setup-vm.sh: the boot daemon, every process of the test home, the home.
VM=${VM:-macOS Explore} H=/Users/harshal/rtest
prlctl exec "$VM" "launchctl bootout system/com.reminal.restore-test 2>/dev/null; rm -f /Library/LaunchDaemons/com.reminal.restore-test.plist
launchctl bootout gui/501/com.reminal.restore-test 2>/dev/null; rm -f /Users/harshal/Library/LaunchAgents/com.reminal.restore-test.plist
for s in cursor-access-token cursor-refresh-token cursor-api-key; do launchctl asuser 501 sudo -u harshal security delete-generic-password -s \$s -a cursor-user >/dev/null 2>&1; done
for sig in TERM KILL; do for p in \$(pgrep -f '$H/'); do [ \$p != \$\$ ] && kill -\$sig \$p 2>/dev/null; done; sleep 1; done
rm -rf $H; ls /Users/harshal | grep -c rtest"
