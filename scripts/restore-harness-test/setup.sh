#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar
#
# First boot of the harness box: every coding agent pointed at fake-llm.mjs,
# past its first-run questions, and hooked up with `reminal integrate` — as a
# person's machine would be after they set it up. Idempotent.
set -e
RIG=${RIG:-/opt/rig}   # where fake-llm.mjs and fake-provider.ts are
# A path as the OS itself writes it — C:\rtest\home\… under Git Bash on
# Windows, where codex keys a trusted folder by it.
wpath() { if command -v cygpath >/dev/null 2>&1; then cygpath -w "$1"; else echo "$1"; fi; }
[ -f $HOME/.setup-done ] && exit 0
M=http://127.0.0.1:8099
KEY=sk-ant-api03-reminal-restore-rig-not-a-real-key-000000000000000000
mkdir -p $HOME/p-claude $HOME/p-codex $HOME/p-gemini $HOME/p-qwen $HOME/p-opencode $HOME/p-pi $HOME/p-cursor

# Environment every session's login shell gets — and so every restored one.
cat > $HOME/.bash_profile <<EOP
export PATH=$HOME/.local/bin:$HOME/bin:$HOME/npm/bin:$HOME/node/bin:\$PATH
export ANTHROPIC_BASE_URL=$M ANTHROPIC_API_KEY=$KEY CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 DISABLE_AUTOUPDATER=1
export FAKE_KEY=not-a-secret IS_SANDBOX=1
export GEMINI_API_KEY=not-a-secret GOOGLE_GEMINI_BASE_URL=$M
export OPENAI_API_KEY=not-a-secret OPENAI_BASE_URL=$M/v1 OPENAI_MODEL=fake-model
EOP
cp $HOME/.bash_profile $HOME/.zprofile   # zsh, the macOS login shell, reads this one

# claude: onboarding done, the key approved, the folders trusted, and the
# bypass-permissions warning accepted once, as a person who uses it has.
node -e '
const k=process.argv[1]; const fs=require("fs");
fs.writeFileSync(process.env.HOME+"/.claude.json", JSON.stringify({hasCompletedOnboarding:true, theme:"dark", bypassPermissionsModeAccepted:true,
  customApiKeyResponses:{approved:[k.slice(-20)], rejected:[]},
  projects:Object.fromEntries(["p-claude","shared-claude","f-claude"].map(d=>process.env.HOME.replace(/\\/g,"/")+"/"+d).map(p=>[p,{hasTrustDialogAccepted:true, hasCompletedProjectOnboarding:true, allowedTools:[]}]))}, null, 2));' "$KEY"

mkdir -p $HOME/.claude
echo '{"skipDangerousModePermissionPrompt":true}' > $HOME/.claude/settings.json

# codex: a provider of its own, the folder trusted.
mkdir -p $HOME/.codex
cat > $HOME/.codex/config.toml <<EOC
model = "fake-model"
model_provider = "fake"
check_for_update_on_startup = false

[model_providers.fake]
name = "fake"
base_url = "$M/v1"
env_key = "FAKE_KEY"
wire_api = "responses"

[projects.'$(wpath $HOME/p-codex)']
trust_level = "trusted"

[projects.'$(wpath $HOME/shared-codex)']
trust_level = "trusted"

[projects.'$(wpath $HOME/f-codex)']
trust_level = "trusted"
EOC

# gemini and qwen: API-key auth, no update checks, folders trusted.
mkdir -p $HOME/.gemini $HOME/.qwen
cat > $HOME/.gemini/settings.json <<'EOG'
{"security":{"auth":{"selectedType":"gemini-api-key"},"folderTrust":{"enabled":false}},
 "general":{"disableAutoUpdate":true,"disableUpdateNag":true},"ui":{"hideTips":true},
 "privacy":{"usageStatisticsEnabled":false},"model":{"name":"fake-model"}}
EOG
echo '{"TRUST_ALL":"TRUST_FOLDER"}' > $HOME/.gemini/trustedFolders.json
cat > $HOME/.qwen/settings.json <<'EOQ'
{"security":{"auth":{"selectedType":"openai"},"folderTrust":{"enabled":false}},
 "general":{"disableAutoUpdate":true},"privacy":{"usageStatisticsEnabled":false},"model":{"name":"fake-model"}}
EOQ

# opencode: an OpenAI-compatible provider.
mkdir -p $HOME/.config/opencode
cat > $HOME/.config/opencode/opencode.json <<EOO
{"\$schema":"https://opencode.ai/config.json","autoupdate":false,"model":"fake/fake-model",
 "provider":{"fake":{"npm":"@ai-sdk/openai-compatible","name":"fake","options":{"baseURL":"$M/v1","apiKey":"not-a-secret"},
 "models":{"fake-model":{"name":"fake model"}}}}}
EOO

# pi: the fake provider as an installed extension and the default model, as
# a person's settings would have it — pi rewrites its process title, so
# flags given on its command line are not visible to carry over.
mkdir -p $HOME/.pi/agent/extensions
cp $RIG/fake-provider.ts $HOME/.pi/agent/extensions/fake-provider.ts
echo '{"defaultProvider":"fake","defaultModel":"fake-1"}' > $HOME/.pi/agent/settings.json

# The hooks that report a conversation id, and the MCP registrations.
reminal integrate -y >$HOME/integrate.log 2>&1 || true
touch $HOME/.setup-done
