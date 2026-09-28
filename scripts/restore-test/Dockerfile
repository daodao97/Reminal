# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar

# A Linux box for `reminal restore`: reminal (built by run.sh), its daemon
# started at "boot" the way launchd/systemd start it at login, and a
# stand-in claude so resuming a conversation can be checked without a login.
FROM debian:trixie-slim
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends bash procps ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY reminal /usr/local/bin/reminal
COPY mcp.sh /usr/local/bin/mcp.sh
COPY claude /usr/local/bin/claude
ENV HOME=/root SHELL=/bin/bash
CMD ["sh", "-c", "reminal daemon >/root/daemon.log 2>&1 & exec sleep infinity"]
