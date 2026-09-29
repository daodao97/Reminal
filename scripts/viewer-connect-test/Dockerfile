# The relay serves the embedded viewer, so a container running it is the
# cheapest way to drive the real page against a real agent.
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates procps && rm -rf /var/lib/apt/lists/*
COPY reminal /usr/local/bin/reminal
ENV REMINAL_RELAY=ws://127.0.0.1:18080/ws REMINAL_WEB=http://127.0.0.1:18080
CMD ["sleep", "infinity"]
