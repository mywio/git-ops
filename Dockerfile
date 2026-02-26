# Build stage
FROM golang:1.24 AS builder
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN make build
RUN make plugins

# Final stage
FROM debian:bookworm-slim

# Install dependencies needed at runtime
RUN apt-get update && apt-get install -y \
    ca-certificates \
    curl \
    docker.io \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /app/bin/git-ops /app/git-ops
COPY --from=builder /app/bin/plugins /app/plugins

ENV PLUGINS_DIR=/app/plugins
ENV PATH="/app:${PATH}"

CMD ["/app/git-ops"]
