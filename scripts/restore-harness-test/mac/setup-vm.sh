#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar
#
# The restore rig on the "macOS Explore" Parallels VM, from the host: the real
# agents (Node, claude, codex, gemini, qwen, opencode, pi, cursor-agent), the
# fake model, and a reminal daemon that starts at boot — all as the VM's user
# under a throwaway home ($H), so nothing of the VM's own reminal or agents is
# touched. Then RIG_TARGET=mac ../check*.sh. Undo with teardown-vm.sh.
#
# The VM does not log in by itself after a reboot, so the daemon is a
# LaunchDaemon run as the user (the product's is a LaunchAgent, at login).
set -e
DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
RIGDIR=$(CDPATH= cd -- "$DIR/.." && pwd)
ROOT=$(CDPATH= cd -- "$RIGDIR/../.." && pwd)
VM=${VM:-macOS Explore} H=/Users/harshal/rtest U=harshal
NODE=${NODE:-v22.20.0}
put() { prlctl exec "$VM" "cat > '$2'" < "$1"; }   # host file → VM path
root() { prlctl exec "$VM" "$1"; }

root "mkdir -p $H/bin $H/rig && chown -R $U $H"
# How the checks reach the VM: a command as base64, run as the user, in a
# login zsh, with the test home and no relay (sessions stay on this VM).
cat > /tmp/runb64.$$ <<EOS
#!/bin/sh
cmd=\$(printf '%s' "\$1" | base64 --decode)
exec sudo -u $U -H env -i HOME=$H USER=$U LOGNAME=$U SHELL=/bin/zsh TERM=xterm-256color LANG=en_US.UTF-8 \\
    PATH=/usr/bin:/bin:/usr/sbin:/sbin REMINAL_RELAY=ws://127.0.0.1:1/ws REMINAL_WEB=http://127.0.0.1:1 \\
    /bin/zsh -lc "\$cmd"
EOS
put /tmp/runb64.$$ $H/runb64; rm -f /tmp/runb64.$$
root "chmod 755 $H/runb64"
bx() { prlctl exec "$VM" "$H/runb64" "$(printf '%s' "$1" | base64)"; }

# reminal, built for the VM, and the rig's helpers.
( cd "$ROOT" && GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o /tmp/reminal-darwin.$$ ./cmd/reminal )
put /tmp/reminal-darwin.$$ $H/bin/reminal; rm -f /tmp/reminal-darwin.$$
for f in mcp.sh sendb.sh rig-procs.sh; do put "$RIGDIR/$f" $H/bin/$f; done
put "$RIGDIR/fake-llm.mjs" $H/rig/fake-llm.mjs
put "$RIGDIR/setup.sh" $H/rig/setup.sh
put "$ROOT/scripts/pi-test/fake-provider.ts" $H/rig/fake-provider.ts
root "chown -R $U $H && chmod 755 $H/bin/*"

# Node, then the agents — the same versions of the world as the Linux rig.
bx "[ -x $H/node/bin/node ] || { curl -fsSL https://nodejs.org/dist/$NODE/node-$NODE-darwin-arm64.tar.gz | tar -xz -C $H && mv $H/node-$NODE-darwin-arm64 $H/node; }"
bx "export PATH=$H/node/bin:\$PATH; npm i -g --silent --prefix $H/npm @anthropic-ai/claude-code @openai/codex @google/gemini-cli @qwen-code/qwen-code opencode-ai @earendil-works/pi-coding-agent >/dev/null 2>&1; ls $H/npm/bin"
bx "[ -x $H/.local/bin/cursor-agent ] || curl -fsS https://cursor.com/install | bash >/dev/null 2>&1; ls $H/.local/bin"

# The agents set up as on the Linux box, then the daemon at boot.
bx "export PATH=$H/bin:$H/npm/bin:$H/node/bin:$H/.local/bin:\$PATH; RIG=$H/rig sh $H/rig/setup.sh; tail -3 $H/integrate.log"
cat > /tmp/boot.$$ <<EOB
#!/bin/sh
# At boot: the fake model, then reminal's daemon — which restores.
export HOME=$H USER=$U LOGNAME=$U SHELL=/bin/zsh LANG=en_US.UTF-8
export PATH=$H/bin:$H/npm/bin:$H/node/bin:$H/.local/bin:/usr/bin:/bin:/usr/sbin:/sbin
export REMINAL_RELAY=ws://127.0.0.1:1/ws REMINAL_WEB=http://127.0.0.1:1
. $H/.zprofile
FAKE_LLM_LOG=$H/fake-llm.log node $H/rig/fake-llm.mjs &
exec reminal daemon >$H/daemon.log 2>&1
EOB
put /tmp/boot.$$ $H/rig/boot.sh; rm -f /tmp/boot.$$
cat > /tmp/plist.$$ <<EOP
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>com.reminal.restore-test</string>
  <key>UserName</key><string>$U</string>
  <key>ProgramArguments</key><array><string>/bin/sh</string><string>$H/rig/boot.sh</string></array>
  <key>RunAtLoad</key><true/>
</dict></plist>
EOP
put /tmp/plist.$$ /Library/LaunchDaemons/com.reminal.restore-test.plist; rm -f /tmp/plist.$$
root "chown -R $U $H; chmod 755 $H/rig/boot.sh; chown root:wheel /Library/LaunchDaemons/com.reminal.restore-test.plist; chmod 644 /Library/LaunchDaemons/com.reminal.restore-test.plist; launchctl bootout system/com.reminal.restore-test 2>/dev/null; launchctl bootstrap system /Library/LaunchDaemons/com.reminal.restore-test.plist && echo daemon-up"
sleep 3
bx 'pgrep -fl "reminal daemon|fake-llm" | head -3'
