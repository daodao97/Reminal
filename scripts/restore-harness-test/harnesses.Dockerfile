# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar

# The real coding agents reminal resumes after a restart, in one box, each
# pointed at fake-llm.mjs so they hold real conversations with no account.
# Built once and cached; run.sh layers reminal on top.
FROM node:22-bookworm
RUN npm i -g --silent @anthropic-ai/claude-code @openai/codex @google/gemini-cli @qwen-code/qwen-code opencode-ai >/dev/null 2>&1 \
    && npm ls -g --depth=0
RUN (curl -fsS https://cursor.com/install | bash) >/dev/null 2>&1 || echo "cursor-agent install failed"
ENV PATH=/root/.local/bin:$PATH
RUN for b in claude codex gemini qwen opencode cursor-agent; do printf '%-13s' $b; ($b --version 2>&1 || echo missing) | head -1; done
RUN npm i -g --silent @earendil-works/pi-coding-agent >/dev/null 2>&1 && printf 'pi           ' && pi --version 2>&1 | head -1
