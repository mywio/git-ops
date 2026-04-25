# Build stage
FROM node:20.19.0-bookworm-slim AS node

FROM golang:1.24 AS builder
WORKDIR /app

COPY --from=node /usr/local/ /usr/local/

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN make build
RUN make plugins

# Final stage
FROM debian:bookworm-slim

# Install dependencies needed at runtime
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    gnupg \
    gosu \
    && install -m 0755 -d /etc/apt/keyrings \
    && curl -fsSL https://download.docker.com/linux/debian/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg \
    && chmod a+r /etc/apt/keyrings/docker.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/debian bookworm stable" > /etc/apt/sources.list.d/docker.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
    docker-ce-cli \
    docker-compose-plugin \
    && groupadd --system git-ops \
    && useradd --system --gid git-ops --home-dir /var/lib/git-ops --create-home --shell /usr/sbin/nologin git-ops \
    && install -d -m 0750 -o git-ops -g git-ops /var/lib/git-ops \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /app/bin/git-ops /app/git-ops
COPY --from=builder /app/bin/plugins /app/plugins
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

RUN chmod 0755 /usr/local/bin/docker-entrypoint.sh

WORKDIR /var/lib/git-ops

ENV HOME=/var/lib/git-ops
ENV STATE_DIR=/var/lib/git-ops
ENV PLUGINS_DIR=/app/plugins
ENV PATH="/app:${PATH}"

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["/app/git-ops"]
