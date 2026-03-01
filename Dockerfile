# syntax=docker/dockerfile:1
# ─────────────────────────────────────────────────────────────────────────────
# Stage 1: build Vite frontend
# ─────────────────────────────────────────────────────────────────────────────
FROM node:22-alpine AS frontend-builder

# Enable pnpm via corepack (matches pnpm v10 used in devcontainer)
RUN corepack enable && corepack prepare pnpm@10 --activate

WORKDIR /app

# Install deps first (layer-cached)
COPY src/frontend/package.json src/frontend/pnpm-lock.yaml src/frontend/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile

# Build
COPY src/frontend/ ./
RUN pnpm build


# ─────────────────────────────────────────────────────────────────────────────
# Stage 2: build Go backend (CGO required by go-libwebp)
# ─────────────────────────────────────────────────────────────────────────────
FROM golang:1.23-bookworm AS backend-builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    libwebp-dev \
    ffmpeg \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Cache go modules
COPY src/backend/go.mod src/backend/go.sum ./
RUN go mod download

# Install goose in its own layer so it isn't re-fetched on every code change
RUN go install github.com/pressly/goose/v3/cmd/goose@latest

COPY src/backend/ ./

# Build app binary
RUN CGO_ENABLED=1 GOOS=linux go build -o wallium .


# ─────────────────────────────────────────────────────────────────────────────
# Stage 2.5: build SVT-AV1 v2.3.0 from source (for Rust worker bindings)
# ─────────────────────────────────────────────────────────────────────────────
FROM debian:bookworm-slim AS svtav1-builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential cmake nasm git ca-certificates \
    && rm -rf /var/lib/apt/lists/*

RUN git clone --depth 1 --branch v2.3.0 https://gitlab.com/AOMediaCodec/SVT-AV1.git /tmp/SVT-AV1 \
    && cd /tmp/SVT-AV1 \
    && mkdir Build && cd Build \
    && cmake .. -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=ON \
       -DBUILD_APPS=OFF -DBUILD_DEC=OFF -DCMAKE_INSTALL_PREFIX=/usr/local \
       -DCMAKE_POLICY_VERSION_MINIMUM=3.5 \
    && make -j$(nproc) \
    && make install \
    && rm -rf /tmp/SVT-AV1


# ─────────────────────────────────────────────────────────────────────────────
# Stage 3: build Rust worker
# ─────────────────────────────────────────────────────────────────────────────
FROM rust:1-bookworm AS worker-builder

# SVT-AV1 v2.3.0 headers + shared library (for svt-av1-enc Rust bindings)
COPY --from=svtav1-builder /usr/local/lib/libSvtAv1Enc* /usr/local/lib/
COPY --from=svtav1-builder /usr/local/lib/pkgconfig/SvtAv1Enc.pc /usr/local/lib/pkgconfig/
COPY --from=svtav1-builder /usr/local/include/svt-av1/ /usr/local/include/svt-av1/
RUN ldconfig

# Install nasm for rav1e SIMD optimizations + libturbojpeg for fast JPEG decode
RUN apt-get update && apt-get install -y --no-install-recommends nasm libturbojpeg0-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Cache deps by building with a dummy main + build.rs first
COPY src/worker/Cargo.toml src/worker/build.rs ./
RUN mkdir src && echo 'fn main() {}' > src/main.rs && cargo build --release && rm -rf src

# Build actual worker
COPY src/worker/ ./
RUN touch src/main.rs && cargo build --release


# ─────────────────────────────────────────────────────────────────────────────
# Stage 4: backend runtime image
# ─────────────────────────────────────────────────────────────────────────────
FROM debian:bookworm-slim AS backend

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    libwebp7 \
    webp \
    ffmpeg \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=backend-builder /app/wallium ./wallium
COPY --from=backend-builder /go/bin/goose ./goose
COPY --from=backend-builder /app/db/migrations ./db/migrations

# Media dirs — overridden at runtime by the shared volume mount
# ./logs is bind-mounted from the host so logrotate can manage it.
RUN mkdir -p \
    ./public/images/thumbnails \
    ./public/videos \
    ./public/upload \
    ./tmp/thumbnails \
    ./logs

EXPOSE 3000
CMD ["./wallium"]


# ─────────────────────────────────────────────────────────────────────────────
# Stage 5: worker runtime image (Rust processing worker)
# ─────────────────────────────────────────────────────────────────────────────
FROM debian:bookworm-slim AS worker

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    ffmpeg \
    util-linux \
    libturbojpeg0 \
    && rm -rf /var/lib/apt/lists/*

# SVT-AV1 v2.3.0 shared library (needed at runtime for in-process AVIF encoding)
COPY --from=svtav1-builder /usr/local/lib/libSvtAv1Enc.so* /usr/local/lib/
RUN ldconfig

WORKDIR /app

COPY --from=worker-builder /app/target/release/wallium-worker ./wallium-worker

# Media dirs — overridden at runtime by the shared volume mount
RUN mkdir -p \
    ./public/images/thumbnails \
    ./public/videos \
    ./public/upload \
    ./tmp/thumbnails

CMD ["./wallium-worker"]


# ─────────────────────────────────────────────────────────────────────────────
# Stage 6: nginx — SPA + /api proxy + static media from shared volume
# ─────────────────────────────────────────────────────────────────────────────
FROM nginx:1.27-alpine AS frontend

# Bake the compiled frontend into the image
COPY --from=frontend-builder /app/dist /var/www/html

# Nginx config
COPY nginx/nginx.conf /etc/nginx/nginx.conf

EXPOSE 80

