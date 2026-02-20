#!/usr/bin/env bash
# dev.sh — start the full ginbar dev stack inside the devcontainer.
#
# What it does:
#   1. Runs goose migrations (idempotent — safe to re-run)
#   2. Starts the Go backend with `air` (hot-reload on save)
#   3. Starts the Vite frontend dev server
#   4. Ctrl-C kills both cleanly
#
# Host access (VS Code forwards these automatically):
#   Frontend : http://localhost:5173   ← open this in your browser
#   Backend  : http://localhost:3000
#   pgAdmin  : http://localhost:5050

set -euo pipefail

# ── colours ──────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; CYAN='\033[0;36m'
YELLOW='\033[1;33m'; BOLD='\033[1m'; RESET='\033[0m'

PG_URL="${DB_URL:-postgres://ginbar:devpassword@localhost:5432/ginbar?sslmode=disable}"
BACKEND_DIR="$(cd "$(dirname "$0")/src/backend" && pwd)"
FRONTEND_DIR="$(cd "$(dirname "$0")/src/frontend" && pwd)"

log()  { echo -e "${CYAN}[dev]${RESET} $*"; }
ok()   { echo -e "${GREEN}[dev]${RESET} $*"; }
warn() { echo -e "${YELLOW}[dev]${RESET} $*"; }
die()  { echo -e "${RED}[dev]${RESET} $*" >&2; exit 1; }

# ── prereq checks ────────────────────────────────────────────────────────────
command -v goose &>/dev/null || die "goose not found — run: go install github.com/pressly/goose/v3/cmd/goose@latest"
command -v air   &>/dev/null || die "air not found — run: go install github.com/air-verse/air@latest"
command -v pnpm  &>/dev/null || die "pnpm not found"

# ── wait for postgres ─────────────────────────────────────────────────────────
log "Waiting for PostgreSQL..."
for i in $(seq 1 30); do
  pg_isready -h localhost -p 5432 -U ginbar -q && break
  [[ $i -eq 30 ]] && die "PostgreSQL did not become ready in time"
  sleep 1
done
ok "PostgreSQL is ready"

# ── migrations ───────────────────────────────────────────────────────────────
log "Running migrations..."
goose -dir "$BACKEND_DIR/db/migrations" postgres "$PG_URL" up
ok "Migrations up-to-date"

# ── start backend (air) ───────────────────────────────────────────────────────
log "Starting backend (air hot-reload)..."
(
  cd "$BACKEND_DIR"
  air
) &
BACKEND_PID=$!

# ── start frontend (vite) ─────────────────────────────────────────────────────
log "Starting frontend (Vite)..."
(
  cd "$FRONTEND_DIR"
  pnpm dev
) &
FRONTEND_PID=$!

# ── banner ───────────────────────────────────────────────────────────────────
sleep 1   # let servers print their own startup lines first
echo ""
echo -e "${BOLD}┌─────────────────────────────────────────────────┐${RESET}"
echo -e "${BOLD}│  ginbar dev stack running                        │${RESET}"
echo -e "${BOLD}│                                                   │${RESET}"
echo -e "${BOLD}│  Frontend  ${GREEN}http://localhost:5173${RESET}${BOLD}              │${RESET}"
echo -e "${BOLD}│  Backend   ${CYAN}http://localhost:3000${RESET}${BOLD}              │${RESET}"
echo -e "${BOLD}│  pgAdmin   ${YELLOW}http://localhost:5050${RESET}${BOLD}              │${RESET}"
echo -e "${BOLD}│                                                   │${RESET}"
echo -e "${BOLD}│  Ctrl-C to stop all processes                     │${RESET}"
echo -e "${BOLD}└─────────────────────────────────────────────────┘${RESET}"
echo ""

# ── wait + trap Ctrl-C ───────────────────────────────────────────────────────
cleanup() {
  echo ""
  warn "Shutting down..."
  kill "$BACKEND_PID"  2>/dev/null || true
  kill "$FRONTEND_PID" 2>/dev/null || true
  wait "$BACKEND_PID"  2>/dev/null || true
  wait "$FRONTEND_PID" 2>/dev/null || true
  ok "All processes stopped."
}
trap cleanup SIGINT SIGTERM

# Block until one of the child processes exits (crash = exit script)
wait -n "$BACKEND_PID" "$FRONTEND_PID" 2>/dev/null || true
cleanup
