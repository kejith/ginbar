#!/usr/bin/env bash
# =============================================================================
# ginbar dev-clean
# Completely resets the local development environment:
#   • Stops all running dev processes (air, vite/pnpm dev, redis)
#   • Drops and recreates the ginbar PostgreSQL database
#   • Re-runs all goose migrations on the fresh database
#   • Wipes media files (images, thumbnails, videos, upload staging)
#   • Wipes the backend tmp/ directory
#
# Does NOT destroy the devcontainer itself or its Docker service volumes.
# Run inside the devcontainer — no sudo required.
#
# Usage:
#   bash scripts/dev-clean.sh           # interactive prompt
#   bash scripts/dev-clean.sh --yes     # skip confirmation (CI / scripted)
# =============================================================================
set -euo pipefail

# ── Colours ──────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'

log()     { echo -e "${CYAN}[dev-clean]${RESET} $*"; }
ok()      { echo -e "${GREEN}[dev-clean]${RESET} $*"; }
warn()    { echo -e "${YELLOW}[dev-clean]${RESET} $*"; }
die()     { echo -e "${RED}[dev-clean]${RESET} $*" >&2; exit 1; }

# ── Paths ─────────────────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(dirname "$SCRIPT_DIR")"
BACKEND_DIR="$REPO_DIR/src/backend"
MEDIA_DIR="${MEDIA_DIR:-$BACKEND_DIR/public}"
TMP_DIR="$BACKEND_DIR/tmp"

# ── Database config (mirrors .devcontainer/docker-compose.yml defaults) ───────
PG_HOST="${PGHOST:-localhost}"
PG_PORT="${PGPORT:-5432}"
PG_USER="${PGUSER:-ginbar}"
PG_PASS="${PGPASSWORD:-devpassword}"
PG_DB="${PGDATABASE:-ginbar}"

export PGPASSWORD="$PG_PASS"

PG_URL="postgres://${PG_USER}:${PG_PASS}@${PG_HOST}:${PG_PORT}/${PG_DB}?sslmode=disable"

# ── Confirmation ──────────────────────────────────────────────────────────────
if [[ "${1:-}" != "--yes" ]]; then
  echo -e "\n${BOLD}${RED}══ ginbar dev-clean — COMPLETE RESET ══${RESET}\n"
  echo " This will permanently wipe:"
  echo ""
  echo -e "   ${RED}•${RESET} PostgreSQL database '${PG_DB}' — all rows, all tables"
  echo -e "   ${RED}•${RESET} Uploaded media:  ${MEDIA_DIR}/images/"
  echo -e "                   ${MEDIA_DIR}/videos/"
  echo -e "                   ${MEDIA_DIR}/upload/"
  echo -e "   ${RED}•${RESET} Backend tmp/:    ${TMP_DIR}/"
  echo ""
  echo " Running dev processes (air, vite, redis) will be stopped."
  echo ""
  echo -e "${RED}${BOLD}This cannot be undone.${RESET}"
  echo ""
  read -rp "Type 'yes' to confirm: " CONFIRM
  [[ "$CONFIRM" != "yes" ]] && { log "Aborted — nothing was changed."; exit 0; }
  echo ""
fi

# ── 1. Stop dev processes ─────────────────────────────────────────────────────
log "Stopping running dev processes…"

# air (Go hot-reload)
if pgrep -f "air" &>/dev/null; then
  pkill -f "air" && ok "air stopped" || warn "Could not stop air (already gone?)"
fi

# pnpm dev / vite
if pgrep -f "pnpm dev\|vite" &>/dev/null; then
  pkill -f "pnpm dev\|vite" && ok "vite stopped" || warn "Could not stop vite (already gone?)"
fi

# built Go backend binary
if pgrep -f "$BACKEND_DIR/tmp/main" &>/dev/null; then
  pkill -f "$BACKEND_DIR/tmp/main" && ok "backend binary stopped" || true
fi

# redis (only stop if we started it from dev.sh; skip if not running)
if redis-cli ping &>/dev/null 2>&1; then
  redis-cli shutdown nosave &>/dev/null 2>&1 && ok "Redis stopped" || warn "Redis shutdown returned non-zero (may be fine)"
else
  log "Redis not running — skipping"
fi

# ── 2. Wait for PostgreSQL ────────────────────────────────────────────────────
log "Waiting for PostgreSQL on ${PG_HOST}:${PG_PORT}…"
for i in $(seq 1 30); do
  pg_isready -h "$PG_HOST" -p "$PG_PORT" -U "$PG_USER" -q && break
  [[ $i -eq 30 ]] && die "PostgreSQL did not become ready in time"
  sleep 1
done
ok "PostgreSQL is ready"

# ── 3. Drop and recreate the database ────────────────────────────────────────
log "Dropping database '${PG_DB}'…"
# Terminate all active connections first so DROP DATABASE succeeds
psql -h "$PG_HOST" -p "$PG_PORT" -U "$PG_USER" -d postgres \
  -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '${PG_DB}' AND pid <> pg_backend_pid();" \
  -q

psql -h "$PG_HOST" -p "$PG_PORT" -U "$PG_USER" -d postgres \
  -c "DROP DATABASE IF EXISTS ${PG_DB};" -q
ok "Database '${PG_DB}' dropped"

log "Creating database '${PG_DB}'…"
psql -h "$PG_HOST" -p "$PG_PORT" -U "$PG_USER" -d postgres \
  -c "CREATE DATABASE ${PG_DB} OWNER ${PG_USER};" -q
ok "Database '${PG_DB}' created"

# ── 4. Run migrations ─────────────────────────────────────────────────────────
command -v goose &>/dev/null || die "goose not found — run: go install github.com/pressly/goose/v3/cmd/goose@latest"
log "Running migrations…"
goose -dir "$BACKEND_DIR/db/migrations" postgres "$PG_URL" up
ok "Migrations applied"

# ── 5. Clear media files ──────────────────────────────────────────────────────
log "Clearing uploaded images and thumbnails…"
if [[ -d "${MEDIA_DIR}/images" ]]; then
  find "${MEDIA_DIR}/images" -mindepth 1 -delete
fi
mkdir -p "${MEDIA_DIR}/images/thumbnails"
ok "Images cleared"

log "Clearing uploaded videos…"
if [[ -d "${MEDIA_DIR}/videos" ]]; then
  find "${MEDIA_DIR}/videos" -mindepth 1 -delete
fi
mkdir -p "${MEDIA_DIR}/videos"
ok "Videos cleared"

log "Clearing upload staging area…"
if [[ -d "${MEDIA_DIR}/upload" ]]; then
  find "${MEDIA_DIR}/upload" -mindepth 1 -delete
fi
mkdir -p "${MEDIA_DIR}/upload"
ok "Upload staging cleared"

# ── 6. Clear backend tmp/ ─────────────────────────────────────────────────────
log "Clearing backend tmp/…"
if [[ -d "$TMP_DIR" ]]; then
  # Preserve the directory itself and the compiled binary placeholder
  find "$TMP_DIR" -mindepth 1 -not -name '.gitkeep' -delete
fi
mkdir -p "$TMP_DIR/thumbnails"
ok "tmp/ cleared"

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
echo -e "${GREEN}${BOLD}══════════════════════════════════════════════${RESET}"
echo -e "${GREEN}${BOLD}  ✓ Dev environment reset successfully.        ${RESET}"
echo -e "${GREEN}${BOLD}  ✓ Fresh database migrated and media wiped.   ${RESET}"
echo -e "${GREEN}${BOLD}══════════════════════════════════════════════${RESET}"
echo ""
echo -e " ${CYAN}Restart the dev stack with:${RESET}"
echo "   bash dev.sh"
echo ""
