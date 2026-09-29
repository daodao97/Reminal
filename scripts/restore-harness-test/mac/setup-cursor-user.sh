#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar
#
# cursor-agent on the macOS VM, as a person has it: it keeps its login in the
# user's DEFAULT keychain, which a throwaway $HOME cannot provide (macOS keeps
# that setting under the user's real home). So the rig gets a user of its own,
# rigtest, whose real home holds a keychain of its own — default, unlocked for
# the rig's processes (runb64 and the boot daemon). Then
#   MAC_USER=rigtest RIG_TARGET=mac ../check*.sh   (cursor ones)
# Undo: teardown-cursor-user.sh.
set -e
DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
RIGDIR=$(CDPATH= cd -- "$DIR/.." && pwd)
ROOT=$(CDPATH= cd -- "$RIGDIR/../.." && pwd)
VM=${VM:-macOS Explore} U=rigtest H=/Users/rigtest NODE=${NODE:-v22.20.0}
put() { prlctl exec "$VM" "cat > '$2'" < "$1"; }
root() { prlctl exec "$VM" "$1"; }
root "id $U >/dev/null 2>&1 || sysadminctl -addUser $U -fullName 'reminal rig' -password rigtest -home $H >/dev/null 2>&1; createhomedir -c -u $U >/dev/null 2>&1; mkdir -p $H/bin $H/rig $H/Library/Keychains; chown -R $U $H; id $U"
cat > /tmp/runb64.$$ <<EOS
#!/bin/sh
cmd=\$(printf '%s' "\$1" | base64 --decode)
exec sudo -u $U -H env -i HOME=$H USER=$U LOGNAME=$U SHELL=/bin/zsh TERM=xterm-256color LANG=en_US.UTF-8 \\
    PATH=/usr/bin:/bin:/usr/sbin:/sbin REMINAL_RELAY=ws://127.0.0.1:1/ws REMINAL_WEB=http://127.0.0.1:1 \\
    /bin/zsh -lc "security unlock-keychain -p rig $H/Library/Keychains/rig.keychain-db 2>/dev/null; \$cmd"
EOS
put /tmp/runb64.$$ $H/runb64; rm -f /tmp/runb64.$$; root "chmod 755 $H/runb64"
bx() { prlctl exec "$VM" "$H/runb64" "$(printf '%s' "$1" | base64)"; }
( cd "$ROOT" && GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o /tmp/reminal-darwin.$$ ./cmd/reminal )
put /tmp/reminal-darwin.$$ $H/bin/reminal; rm -f /tmp/reminal-darwin.$$
for f in mcp.sh sendb.sh rig-procs.sh; do put "$RIGDIR/$f" $H/bin/$f; done
put "$RIGDIR/fake-llm.mjs" $H/rig/fake-llm.mjs
root "chown -R $U $H; chmod 755 $H/bin/*"
# Its keychain: made, never auto-locked, the user's default and search list.
bx 'K=$HOME/Library/Keychains/rig.keychain-db; [ -f $K ] || security create-keychain -p rig $K; security set-keychain-settings $K; security unlock-keychain -p rig $K; security list-keychains -d user -s $K; security default-keychain -d user -s $K; echo "default keychain: $(security default-keychain -d user)"'
bx "[ -x $H/node/bin/node ] || { curl -fsSL https://nodejs.org/dist/$NODE/node-$NODE-darwin-arm64.tar.gz | tar -xz -C $H && mv $H/node-$NODE-darwin-arm64 $H/node; }; $H/node/bin/node --version"
bx "[ -x $H/.local/bin/cursor-agent ] || curl -fsS https://cursor.com/install | bash >/dev/null 2>&1; ls $H/.local/bin"
printf 'export PATH=%s/bin:%s/node/bin:%s/.local/bin:$PATH\n' $H $H $H > /tmp/zp.$$; put /tmp/zp.$$ $H/.zprofile; rm -f /tmp/zp.$$
cat > /tmp/boot.$$ <<EOB
#!/bin/sh
export HOME=$H USER=$U LOGNAME=$U SHELL=/bin/zsh LANG=en_US.UTF-8
export PATH=$H/bin:$H/node/bin:$H/.local/bin:/usr/bin:/bin:/usr/sbin:/sbin
export REMINAL_RELAY=ws://127.0.0.1:1/ws REMINAL_WEB=http://127.0.0.1:1
security unlock-keychain -p rig $H/Library/Keychains/rig.keychain-db
exec reminal daemon >$H/daemon.log 2>&1
EOB
put /tmp/boot.$$ $H/rig/boot.sh; rm -f /tmp/boot.$$
sed "s|com.reminal.restore-test|com.reminal.restore-test-cursor|; s|<string>harshal</string>|<string>$U</string>|; s|/Users/harshal/rtest/rig/boot.sh|$H/rig/boot.sh|" <<'EOP' > /tmp/plist.$$
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>com.reminal.restore-test</string>
  <key>UserName</key><string>harshal</string>
  <key>ProgramArguments</key><array><string>/bin/sh</string><string>/Users/harshal/rtest/rig/boot.sh</string></array>
  <key>RunAtLoad</key><true/>
</dict></plist>
EOP
put /tmp/plist.$$ /Library/LaunchDaemons/com.reminal.restore-test-cursor.plist; rm -f /tmp/plist.$$
root "chown -R $U $H; chmod 755 $H/rig/boot.sh; chown root:wheel /Library/LaunchDaemons/com.reminal.restore-test-cursor.plist; chmod 644 /Library/LaunchDaemons/com.reminal.restore-test-cursor.plist; launchctl bootout system/com.reminal.restore-test-cursor 2>/dev/null; launchctl bootstrap system /Library/LaunchDaemons/com.reminal.restore-test-cursor.plist && echo daemon-up"
