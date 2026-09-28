# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar
FROM reminal-harnesses
RUN mkdir -p /opt/rig
COPY reminal mcp.sh sendb.sh /usr/local/bin/
COPY fake-llm.mjs setup.sh fake-provider.ts /opt/rig/
ENV HOME=/root SHELL=/bin/bash
# A boot: the model, first-run setup, then the daemon — which restores.
CMD ["sh", "-c", "node /opt/rig/fake-llm.mjs & bash /opt/rig/setup.sh; PATH=/root/.local/bin:$PATH reminal daemon >/root/daemon.log 2>&1 & exec sleep infinity"]
