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
RUN CGO_ENABLED=1 GOOS=linux go build -o ginbar .


# ─────────────────────────────────────────────────────────────────────────────
# Stage 3: backend runtime image
# ─────────────────────────────────────────────────────────────────────────────
FROM debian:bookworm-slim AS backend

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    libwebp7 \
    webp \
    ffmpeg \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=backend-builder /app/ginbar ./ginbar
COPY --from=backend-builder /go/bin/goose ./goose
COPY --from=backend-builder /app/db/migrations ./db/migrations

# Media dirs — overridden at runtime by the shared volume mount
RUN mkdir -p \
    ./public/images/thumbnails \
    ./public/videos \
    ./public/upload \
    ./tmp/thumbnails

EXPOSE 3000
CMD ["./ginbar"]


# ─────────────────────────────────────────────────────────────────────────────
# Stage 4: nginx — SPA + /api proxy + static media from shared volume
# ─────────────────────────────────────────────────────────────────────────────
FROM nginx:1.27-alpine AS frontend

# Bake the compiled frontend into the image
COPY --from=frontend-builder /app/dist /var/www/html

# Nginx config
COPY nginx/nginx.conf /etc/nginx/nginx.conf

EXPOSE 80

