# The throwaway world the Windows rig runs in: its own profile folder, its
# own PATH, no relay (sessions stay on the VM), agents pointed at fake-llm.
$R = "C:\rtest"; $HH = "$R\home"
$env:USERPROFILE = $HH; $env:HOME = $HH
$env:APPDATA = "$HH\AppData\Roaming"; $env:LOCALAPPDATA = "$HH\AppData\Local"
$env:PATH = "$R\bin;$R\npm;$R\node;$HH\AppData\Local\cursor-agent;$HH\.local\bin;$R\git\bin;$R\git\usr\bin;" + $env:PATH
$env:npm_config_prefix = "$R\npm"
# codex (Rust) finds the profile folder through Windows, not USERPROFILE.
$env:CODEX_HOME = "$HH\.codex"
$env:CLAUDE_CODE_GIT_BASH_PATH = "$R\git\bin\bash.exe"
$env:REMINAL_RELAY = "ws://127.0.0.1:1/ws"; $env:REMINAL_WEB = "http://127.0.0.1:1"
$M = "http://127.0.0.1:8099"
$env:ANTHROPIC_BASE_URL = $M; $env:ANTHROPIC_API_KEY = "sk-ant-api03-reminal-restore-rig-not-a-real-key-000000000000000000"
$env:CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC = "1"; $env:DISABLE_AUTOUPDATER = "1"; $env:IS_SANDBOX = "1"
$env:FAKE_KEY = "not-a-secret"; $env:GEMINI_API_KEY = "not-a-secret"; $env:GOOGLE_GEMINI_BASE_URL = $M
$env:OPENAI_API_KEY = "not-a-secret"; $env:OPENAI_BASE_URL = "$M/v1"; $env:OPENAI_MODEL = "fake-model"
$env:FAKE_LLM_LOG = "$HH\fake-llm.log"
$ProgressPreference = "SilentlyContinue"
