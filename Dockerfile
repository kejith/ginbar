# ================================================
# | NPM BUILDER - FRONTEND
# ================================================

# Use Node 20 LTS (modern version)
FROM node:20-alpine AS npm_builder

# Set the working directory
WORKDIR /app

# Copy package files
COPY ./frontend/package*.json ./

# Install dependencies
RUN npm ci --only=production

# Copy the rest of the application code
COPY ./frontend .

# Build the React app
RUN npm run build

# ================================================
# | GO BUILDER - BACKEND
# ================================================

# Use Go 1.22 (latest stable)
FROM golang:1.22-bookworm AS go_builder

# Install required system packages
RUN apt-get update && apt-get install -y \
    libwebp-dev \
    ffmpeg \
    webp \
    && rm -rf /var/lib/apt/lists/*

# Set the working directory
WORKDIR /app

# Copy go mod files
COPY ./backend/go.mod ./backend/go.sum ./
RUN go mod download

# Copy the backend source code
COPY ./backend .

# Build the Go project
RUN CGO_ENABLED=1 GOOS=linux go build -o ginbar -a -ldflags '-linkmode external -extldflags "-static"' .

# ================================================
# | FINAL STAGE
# ================================================

FROM debian:bookworm-slim

# Install runtime dependencies
RUN apt-get update && apt-get install -y \
    ca-certificates \
    libwebp7 \
    ffmpeg \
    && rm -rf /var/lib/apt/lists/*

# Set the working directory
WORKDIR /app

# Copy the built frontend from npm_builder
COPY --from=npm_builder /app/build ./public

# Copy the built Go binary from go_builder
COPY --from=go_builder /app/ginbar .

# Create necessary directories
RUN mkdir -p ./tmp/thumbnails ./public/images

# Expose port
EXPOSE 3000

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["./ginbar", "-health"] || exit 1

# Run the application
CMD ["./ginbar"]
