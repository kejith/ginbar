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
    nasm \
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
# v3.27.0 requires go >= 1.25; pin to last release compatible with Go 1.23.
RUN go install github.com/pressly/goose/v3/cmd/goose@v3.24.2

# ── Rust (stable) ─────────────────────────────────────────────────────────────
# Install for all users by setting CARGO_HOME + RUSTUP_HOME to /usr/local.
# This keeps Rust on PATH for both root and vscode without per-user init files.
ENV RUSTUP_HOME=/usr/local/rustup \
    CARGO_HOME=/usr/local/cargo \
    PATH=/usr/local/cargo/bin:$PATH
RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs \
    | sh -s -- -y --no-modify-path --default-toolchain stable \
 && rustup component add clippy rustfmt \
 && chmod -R a+w /usr/local/rustup /usr/local/cargo

# ── SVT-AV1 v2.3.0 (built from source for Rust svt-av1-enc bindings) ─────────
# Builds a shared library so the worker can link against it at dev/bench time.
RUN apt-get update && apt-get install -y --no-install-recommends cmake \
 && rm -rf /var/lib/apt/lists/* \
 && git clone --depth 1 --branch v2.3.0 https://gitlab.com/AOMediaCodec/SVT-AV1.git /tmp/SVT-AV1 \
 && cd /tmp/SVT-AV1 && mkdir -p Build && cd Build \
 && cmake .. -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=ON \
    -DBUILD_APPS=OFF -DBUILD_DEC=OFF -DCMAKE_INSTALL_PREFIX=/usr/local \
    -DCMAKE_POLICY_VERSION_MINIMUM=3.5 \
 && make -j$(nproc) \
 && make install \
 && ldconfig \
 && rm -rf /tmp/SVT-AV1

USER vscode
