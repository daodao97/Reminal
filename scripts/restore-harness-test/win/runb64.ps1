# How the checks reach the Windows VM: a POSIX command as base64, run in Git
# Bash inside the rig's world (env.ps1). prlctl strips quotes and Windows
# PowerShell 5.1 mangles them on the way to a native program, so the command
# reaches bash in a file, never as an argument.
param([string]$b64)
. C:\rtest\env.ps1
$f = "C:\rtest\cmd-$PID.sh"
[IO.File]::WriteAllBytes($f, [Convert]::FromBase64String($b64))
& "C:\rtest\git\bin\bash.exe" -l $f
$rc = $LASTEXITCODE
Remove-Item $f -ErrorAction SilentlyContinue
exit $rc
