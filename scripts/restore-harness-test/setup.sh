#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar
#
# First boot of the harness box: every coding agent pointed at fake-llm.mjs,
# past its first-run questions, and hooked up with `reminal integrate` — as a
# person's machine would be after they set it up. Idempotent.
set -e
[ -f /root/.setup-done ] && exit 0
M=http://127.0.0.1:8099
KEY=sk-ant-api03-reminal-restore-rig-not-a-real-key-000000000000000000
mkdir -p /root/p-claude /root/p-codex /root/p-gemini /root/p-qwen /root/p-opencode /root/p-pi /root/p-cursor

# Environment every session's login shell gets — and so every restored one.
cat > /root/.bash_profile <<EOP
export PATH=/root/.local/bin:\$PATH
export ANTHROPIC_BASE_URL=$M ANTHROPIC_API_KEY=$KEY CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 DISABLE_AUTOUPDATER=1
export FAKE_KEY=not-a-secret IS_SANDBOX=1
export GEMINI_API_KEY=not-a-secret GOOGLE_GEMINI_BASE_URL=$M
export OPENAI_API_KEY=not-a-secret OPENAI_BASE_URL=$M/v1 OPENAI_MODEL=fake-model
EOP

# claude: onboarding done, the key approved, the folders trusted, and the
# bypass-permissions warning accepted once, as a person who uses it has.
node -e '
const k=process.argv[1]; const fs=require("fs");
fs.writeFileSync("/root/.claude.json", JSON.stringify({hasCompletedOnboarding:true, theme:"dark", bypassPermissionsModeAccepted:true,
  customApiKeyResponses:{approved:[k.slice(-20)], rejected:[]},
  projects:Object.fromEntries(["/root/p-claude","/root/shared-claude","/root/f-claude"].map(p=>[p,{hasTrustDialogAccepted:true, hasCompletedProjectOnboarding:true, allowedTools:[]}]))}, null, 2));' "$KEY"

mkdir -p /root/.claude
echo '{"skipDangerousModePermissionPrompt":true}' > /root/.claude/settings.json

# codex: a provider of its own, the folder trusted.
mkdir -p /root/.codex
cat > /root/.codex/config.toml <<EOC
model = "fake-model"
model_provider = "fake"
check_for_update_on_startup = false

[model_providers.fake]
name = "fake"
base_url = "$M/v1"
env_key = "FAKE_KEY"
wire_api = "responses"

[projects."/root/p-codex"]
trust_level = "trusted"

[projects."/root/shared-codex"]
trust_level = "trusted"

[projects."/root/f-codex"]
trust_level = "trusted"
EOC

# gemini and qwen: API-key auth, no update checks, folders trusted.
mkdir -p /root/.gemini /root/.qwen
cat > /root/.gemini/settings.json <<'EOG'
{"security":{"auth":{"selectedType":"gemini-api-key"},"folderTrust":{"enabled":false}},
 "general":{"disableAutoUpdate":true,"disableUpdateNag":true},"ui":{"hideTips":true},
 "privacy":{"usageStatisticsEnabled":false},"model":{"name":"fake-model"}}
EOG
echo '{"TRUST_ALL":"TRUST_FOLDER"}' > /root/.gemini/trustedFolders.json
cat > /root/.qwen/settings.json <<'EOQ'
{"security":{"auth":{"selectedType":"openai"},"folderTrust":{"enabled":false}},
 "general":{"disableAutoUpdate":true},"privacy":{"usageStatisticsEnabled":false},"model":{"name":"fake-model"}}
EOQ

# opencode: an OpenAI-compatible provider.
mkdir -p /root/.config/opencode
cat > /root/.config/opencode/opencode.json <<EOO
{"\$schema":"https://opencode.ai/config.json","autoupdate":false,"model":"fake/fake-model",
 "provider":{"fake":{"npm":"@ai-sdk/openai-compatible","name":"fake","options":{"baseURL":"$M/v1","apiKey":"not-a-secret"},
 "models":{"fake-model":{"name":"fake model"}}}}}
EOO

# pi: the fake provider as an installed extension and the default model, as
# a person's settings would have it — pi rewrites its process title, so
# flags given on its command line are not visible to carry over.
mkdir -p /root/.pi/agent/extensions
cp /opt/rig/fake-provider.ts /root/.pi/agent/extensions/fake-provider.ts
echo '{"defaultProvider":"fake","defaultModel":"fake-1"}' > /root/.pi/agent/settings.json

# The hooks that report a conversation id, and the MCP registrations.
reminal integrate -y >/root/integrate.log 2>&1 || true
touch /root/.setup-done
