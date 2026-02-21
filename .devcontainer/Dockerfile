FROM mcr.microsoft.com/devcontainers/go:1-1.23-bookworm

# ── System tools + CGO deps ───────────────────────────────────────────────────
# Node is installed via devcontainer features, not here.
# Remove stale yarn apt repo baked into the base image (expired GPG key).
RUN rm -f /etc/apt/sources.list.d/yarn.list \
 && apt-get update && apt-get install -y --no-install-recommends \
    jq \
    httpie \
    postgresql-client \
    libwebp-dev \
    ffmpeg \
    webp \
    make \
    git \
    curl \
    unzip \
    procps \
    redis-server \
 && rm -rf /var/lib/apt/lists/*

# ── sqlc (latest) ─────────────────────────────────────────────────────────────
RUN SQLC_VERSION=$(curl -s https://api.github.com/repos/sqlc-dev/sqlc/releases/latest | jq -r .tag_name) \
 && curl -fsSL "https://downloads.sqlc.dev/sqlc_${SQLC_VERSION#v}_linux_amd64.tar.gz" | tar -xz -C /usr/local/bin sqlc

# ── golang-migrate ────────────────────────────────────────────────────────────
RUN MIGRATE_VERSION=$(curl -s https://api.github.com/repos/golang-migrate/migrate/releases/latest | jq -r .tag_name) \
 && curl -fsSL "https://github.com/golang-migrate/migrate/releases/download/${MIGRATE_VERSION}/migrate.linux-amd64.tar.gz" | tar -xz -C /usr/local/bin migrate

# ── air (Go hot-reload) — pinned to last release compatible with Go 1.23 ──────
RUN go install github.com/air-verse/air@v1.52.3

# ── golangci-lint ─────────────────────────────────────────────────────────────
RUN curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b /usr/local/bin

# ── goose (alternative migrate, nice SQL-first feel) ─────────────────────────
RUN go install github.com/pressly/goose/v3/cmd/goose@latest

USER vscode
