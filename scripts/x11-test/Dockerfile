# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 Harshal Gajjar

# A throwaway X11 desktop for exercising reminal's Linux window backend.
#
# The backend shells out to wmctrl, xdotool and ImageMagick against a real X
# server with a real window manager, so none of it can be tested on macOS and
# none of it can be faked. This image gives it exactly that, headlessly, in a few
# hundred megabytes — which beats testing on a 498 MB cloud VM that falls over
# when you install packages on it.
#
# See scripts/x11-test/run.sh for the one-command entry point.

FROM golang:1.26-bookworm

RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        xvfb \
        x11-utils \
        openbox \
        xterm \
        wmctrl \
        xdotool \
        imagemagick \
    && rm -rf /var/lib/apt/lists/*

# ImageMagick 6 on Debian ships a policy that can block `import`; the backend
# depends on it, and a policy denial looks like a capture bug rather than a
# configuration one. Drop the screenshot restrictions in this throwaway image.
RUN for f in /etc/ImageMagick-6/policy.xml /etc/ImageMagick-7/policy.xml; do \
        [ -f "$f" ] && sed -i 's/rights="none"/rights="read|write"/g' "$f" || true; \
    done

ENV DISPLAY=:99 \
    REMINAL_X11_TEST=1 \
    GOFLAGS=-buildvcs=false

COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

WORKDIR /src
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
