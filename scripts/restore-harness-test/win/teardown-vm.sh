#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar
# Undo setup-vm.sh: the boot task, every process running from C:\rtest, the
# PATH entries (put back as they were), the folder. One step per command: a
# single long -EncodedCommand is more than prlctl will pass.
VM=${VM:-Windows 11}
ps1() { prlctl exec "$VM" powershell -NoProfile -EncodedCommand "$(printf '$ProgressPreference="SilentlyContinue"; %s' "$1" | iconv -t UTF-16LE | base64)" 2>&1 | tr -d '\r' | grep -v "CLIXML\|<Objs"; }
ps1 'schtasks /Delete /F /TN reminal-restore-test 2>$null | Out-Null; "boot task removed"'
ps1 'Get-CimInstance Win32_Process | ? { $_.ExecutablePath -like "C:\rtest\*" -or $_.CommandLine -like "*C:\rtest\*" } | ? { $_.ProcessId -ne $PID } | % { Stop-Process -Id $_.ProcessId -Force -EA SilentlyContinue }; Start-Sleep 2; "processes stopped"'
ps1 '$k="HKCU:\Environment"; $b=Get-ItemProperty $k -Name RigPathBackup -EA SilentlyContinue; if ($b) { $p=(($b.RigPathBackup -split ";") | ? { $_ -and $_ -notlike "C:\rtest\*" }) -join ";"; if ($p) { Set-ItemProperty $k -Name Path -Type ExpandString -Value $p } else { Remove-ItemProperty $k -Name Path -EA SilentlyContinue }; Remove-ItemProperty $k -Name RigPathBackup }; "PATH restored"'
ps1 'Remove-Item -Recurse -Force C:\rtest -EA SilentlyContinue; "C:\rtest left: " + (Test-Path C:\rtest)'
