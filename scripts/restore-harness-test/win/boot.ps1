# At boot (a scheduled task, as SYSTEM): the fake model, then reminal's
# daemon — which restores.
. C:\rtest\env.ps1
Start-Process -WindowStyle Hidden -FilePath "C:\rtest\node\node.exe" -ArgumentList "C:\rtest\rig\fake-llm.mjs"
& "C:\rtest\bin\reminal.exe" daemon *> "C:\rtest\home\daemon.log"
