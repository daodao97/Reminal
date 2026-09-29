#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar
# Rebuild reminal.exe and swap it into the Windows rig: sessions stopped, restore
# records cleared, the boot daemon started again. (A running .exe is locked.)
cd ~/Desktop/server/GitHub/reminal-restore
ps() { prlctl exec "Windows 11" powershell -NoProfile -ExecutionPolicy Bypass -EncodedCommand "$(printf '$ProgressPreference="SilentlyContinue"; %s' "$1" | iconv -t UTF-16LE | base64)"; }
put() { ps "\$i=[Console]::OpenStandardInput(); \$o=[IO.File]::Create('$2'); \$i.CopyTo(\$o); \$o.Close()" < "$1"; }
GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build -o /tmp/reminal-win.exe ./cmd/reminal || exit 1
ps 'Get-CimInstance Win32_Process | ? { $_.ExecutablePath -like "C:\rtest\bin\reminal.exe" -or $_.CommandLine -like "*C:\rtest\home\p-*" -or $_.CommandLine -like "*C:\rtest\home\shared-*" -or $_.CommandLine -like "*C:\rtest\home\f-*" } | % { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }; Start-Sleep 2; Remove-Item C:\rtest\home\.reminal\restore -Recurse -Force -ErrorAction SilentlyContinue' >/dev/null
put /tmp/reminal-win.exe 'C:\rtest\bin\reminal.exe'; rm -f /tmp/reminal-win.exe
ps 'schtasks /Run /TN reminal-restore-test | Out-Null; Start-Sleep 4; "daemon " + (Get-CimInstance Win32_Process | ? { $_.CommandLine -match "reminal.exe.? daemon" }).ProcessId' | tr -d '\r' | tail -1
