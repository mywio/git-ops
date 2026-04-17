# Build stage
FROM node:20.19.0-bookworm-slim AS node

FROM golang:1.24 AS builder
WORKDIR /app

COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/bin/npm /usr/local/bin/npm
COPY --from=node /usr/local/bin/npx /usr/local/bin/npx
COPY --from=node /usr/local/lib/node_modules /usr/local/lib/node_modules

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
    && install -m 0755 -d /etc/apt/keyrings \
    && curl -fsSL https://download.docker.com/linux/debian/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg \
    && chmod a+r /etc/apt/keyrings/docker.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/debian bookworm stable" > /etc/apt/sources.list.d/docker.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
    docker-ce-cli \
    docker-compose-plugin \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /app/bin/git-ops /app/git-ops
COPY --from=builder /app/bin/plugins /app/plugins

ENV PLUGINS_DIR=/app/plugins
ENV PATH="/app:${PATH}"

CMD ["/app/git-ops"]
