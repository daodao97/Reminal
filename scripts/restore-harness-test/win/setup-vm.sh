#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar
#
# The restore rig on the "Windows 11" Parallels VM, from the host. prlctl runs
# as SYSTEM, so everything does — in C:\rtest, with its own profile folder
# (env.ps1), so the VM's own reminal and SYSTEM's real profile are untouched.
# Portable Node and Git (claude needs Git Bash, and the checks' commands run
# in it), the agents, the fake model, and a daemon started at boot by a
# scheduled task. Then RIG_TARGET=win ../check*.sh. Undo with teardown-vm.sh.
# No set -e: PowerShell reports a pipeline cut short (| select -First 1) as a
# failure. Each step prints what it did; the checks see what did not happen.
DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
RIGDIR=$(CDPATH= cd -- "$DIR/.." && pwd)
ROOT=$(CDPATH= cd -- "$RIGDIR/../.." && pwd)
VM=${VM:-Windows 11}
NODE=${NODE:-v22.20.0}
ps() { prlctl exec "$VM" powershell -NoProfile -ExecutionPolicy Bypass -EncodedCommand "$(printf '$ProgressPreference="SilentlyContinue"; %s' "$1" | iconv -t UTF-16LE | base64)"; }
put() { ps "\$i=[Console]::OpenStandardInput(); \$o=[IO.File]::Create('$2'); \$i.CopyTo(\$o); \$o.Close()" < "$1"; }

ps 'foreach ($d in "C:\rtest\bin","C:\rtest\rig","C:\rtest\home\AppData\Roaming","C:\rtest\home\AppData\Local","C:\rtest\npm") { New-Item -ItemType Directory -Force $d | Out-Null }'
for f in env.ps1 runb64.ps1 boot.ps1; do put "$DIR/$f" "C:\\rtest\\$f"; done
( cd "$ROOT" && GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build -o /tmp/reminal-win.$$ ./cmd/reminal )
put /tmp/reminal-win.$$ 'C:\rtest\bin\reminal.exe'; rm -f /tmp/reminal-win.$$
for f in mcp.sh sendb.sh rig-procs.sh; do put "$RIGDIR/$f" "C:\\rtest\\bin\\$f"; done
put "$RIGDIR/fake-llm.mjs" 'C:\rtest\rig\fake-llm.mjs'
put "$RIGDIR/setup.sh" 'C:\rtest\rig\setup.sh'
put "$ROOT/scripts/pi-test/fake-provider.ts" 'C:\rtest\rig\fake-provider.ts'

# Node and Git, portable.
ps "if (!(Test-Path C:\rtest\node\node.exe)) { Invoke-WebRequest https://nodejs.org/dist/$NODE/node-$NODE-win-arm64.zip -OutFile C:\rtest\node.zip; Expand-Archive C:\rtest\node.zip C:\rtest -Force; Rename-Item C:\rtest\node-$NODE-win-arm64 node; Remove-Item C:\rtest\node.zip }; & C:\rtest\node\node.exe --version"
ps 'if (!(Test-Path C:\rtest\git\bin\bash.exe)) { $a = (Invoke-RestMethod https://api.github.com/repos/git-for-windows/git/releases/latest).assets | ? name -like "PortableGit-*-arm64.7z.exe" | select -First 1; Invoke-WebRequest $a.browser_download_url -OutFile C:\rtest\pgit.exe; Start-Process -Wait C:\rtest\pgit.exe -ArgumentList "-oC:\rtest\git","-y"; Remove-Item C:\rtest\pgit.exe }; & C:\rtest\git\bin\bash.exe --version | select -First 1'

# The agents.
# Through cmd: Windows PowerShell 5.1 turns npm's stderr notices into errors.
ps '. C:\rtest\env.ps1; cmd /c "C:\rtest\node\npm.cmd i -g --silent --no-update-notifier @anthropic-ai/claude-code @openai/codex @google/gemini-cli @qwen-code/qwen-code opencode-ai @earendil-works/pi-coding-agent >nul 2>&1"; Get-ChildItem C:\rtest\npm -Filter *.cmd | % BaseName'
# cursor-agent's native modules need the Visual C++ runtime (most Windows
# machines have it; a fresh VM does not).
ps 'Invoke-WebRequest https://aka.ms/vs/17/release/vc_redist.arm64.exe -OutFile C:\rtest\vcr.exe; (Start-Process -Wait -PassThru C:\rtest\vcr.exe -ArgumentList "/install","/quiet","/norestart").ExitCode; Remove-Item C:\rtest\vcr.exe'
ps '. C:\rtest\env.ps1; if (!(Get-Command cursor-agent -ErrorAction SilentlyContinue)) { try { irm "https://cursor.com/install?win32=true" | iex *> $null } catch {} }; (Get-Command cursor-agent -ErrorAction SilentlyContinue).Source' || true

# On the PATH where an installer puts it: reminal rebuilds a Windows session's
# PATH from the registry, as a freshly opened terminal would, so a PATH set
# only in this process never reaches the session's shell. The value before is
# kept (RigPathBackup) for teardown-vm.sh to put back.
ps '$k = "HKCU:\Environment"; $old = (Get-ItemProperty $k -Name Path -ErrorAction SilentlyContinue).Path; if ($old -notlike "*C:\rtest\npm*") { Set-ItemProperty $k -Name RigPathBackup -Value ([string]$old); $add = "C:\rtest\bin;C:\rtest\npm;C:\rtest\node;C:\rtest\home\AppData\Local\cursor-agent;C:\rtest\git\bin"; Set-ItemProperty $k -Name Path -Type ExpandString -Value ($add + $(if ($old) { ";" + $old } else { "" })) }; (Get-ItemProperty $k -Name Path).Path'

# Configured as on the other boxes, then the daemon at boot.
bx() { prlctl exec "$VM" powershell -NoProfile -ExecutionPolicy Bypass -File 'C:\rtest\runb64.ps1' "$(printf '%s' "$1" | base64)"; }
bx 'RIG=C:/rtest/rig sh C:/rtest/rig/setup.sh && tail -4 $HOME/integrate.log'
ps 'schtasks /Create /F /TN reminal-restore-test /SC ONSTART /RU SYSTEM /TR "powershell -NoProfile -ExecutionPolicy Bypass -File C:\rtest\boot.ps1" | Out-Null; schtasks /Run /TN reminal-restore-test | Out-Null; Start-Sleep 4; Get-CimInstance Win32_Process | ? { $_.CommandLine -match "reminal.exe.? daemon|fake-llm" } | % { $_.ProcessId.ToString() + " " + $_.Name }'
