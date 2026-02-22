#!/usr/bin/env bash
# =============================================================================
# wallium clean
# Wipes the PostgreSQL database (drops the pgdata volume) and deletes all
# uploaded images, thumbnails, and videos from the media directory.
#
# THIS IS DESTRUCTIVE — use only when you want to completely reset the data
# on a deployed instance (e.g. after a test run or before going live).
#
# Run as root or with sudo.
# =============================================================================
set -euo pipefail

# ── Colours ──────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'

info()    { echo -e "${CYAN}→${RESET} $*"; }
success() { echo -e "${GREEN}✓${RESET} $*"; }
warn()    { echo -e "${YELLOW}!${RESET} $*"; }
error()   { echo -e "${RED}✗${RESET} $*" >&2; exit 1; }

# ── Root check ───────────────────────────────────────────────────────────────
[[ "$EUID" -ne 0 ]] && error "Please run as root: sudo bash scripts/clean.sh"

INSTALL_DIR="${WALLIUM_DIR:-/opt/wallium}"
[[ -d "$INSTALL_DIR" ]] || error "Install directory not found: $INSTALL_DIR (set \$WALLIUM_DIR to override)"
cd "$INSTALL_DIR"

# Source .env so MEDIA_DIR is resolved
[[ -f "$INSTALL_DIR/.env" ]] && { set -a; source "$INSTALL_DIR/.env"; set +a; }
MEDIA_DIR="${MEDIA_DIR:-${INSTALL_DIR}/media}"

# ── Warning banner ────────────────────────────────────────────────────────────
echo -e "\n${BOLD}${RED}══ wallium clean — DESTRUCTIVE OPERATION ══${RESET}\n"
echo " This will permanently delete:"
echo ""
echo -e "   ${RED}•${RESET} PostgreSQL database — all posts, users, comments, votes, tags"
echo -e "   ${RED}•${RESET} Uploaded images and thumbnails:  ${MEDIA_DIR}/images/"
echo -e "   ${RED}•${RESET} Uploaded videos:                 ${MEDIA_DIR}/videos/"
echo -e "   ${RED}•${RESET} Upload staging area:             ${MEDIA_DIR}/upload/"
echo ""
echo -e " The Docker stack will be stopped, wiped, and restarted with a fresh database."
echo ""
echo -e "${RED}${BOLD}This cannot be undone.${RESET}"
echo ""
read -rp "Type 'yes' to confirm: " CONFIRM
[[ "$CONFIRM" != "yes" ]] && { info "Aborted — nothing was changed."; exit 0; }
echo ""

# ── 1. Stop the stack and remove named volumes (pgdata) ──────────────────────
info "Stopping stack and removing database volume…"
docker compose down -v --remove-orphans
success "Stack stopped and pgdata volume removed"

# ── 2. Wipe uploaded media files ─────────────────────────────────────────────
info "Clearing uploaded images and thumbnails…"
if [[ -d "${MEDIA_DIR}/images" ]]; then
  find "${MEDIA_DIR}/images" -mindepth 1 -delete
fi
mkdir -p "${MEDIA_DIR}/images/thumbnails"
success "Images cleared"

info "Clearing uploaded videos…"
if [[ -d "${MEDIA_DIR}/videos" ]]; then
  find "${MEDIA_DIR}/videos" -mindepth 1 -delete
fi
mkdir -p "${MEDIA_DIR}/videos"
success "Videos cleared"

info "Clearing upload staging area…"
if [[ -d "${MEDIA_DIR}/upload" ]]; then
  find "${MEDIA_DIR}/upload" -mindepth 1 -delete
fi
mkdir -p "${MEDIA_DIR}/upload"
success "Upload staging cleared"

# Restore permissions
chmod -R 755 "$MEDIA_DIR"

# ── 3. Start only postgres + redis so migrations can run before the backend ───
info "Starting postgres and redis…"
docker compose up -d --remove-orphans postgres redis

# Wait for postgres to become healthy before running migrations
info "Waiting for PostgreSQL to be ready…"
for i in $(seq 1 30); do
  if docker compose exec -T postgres pg_isready -q 2>/dev/null; then
    break
  fi
  sleep 2
done

# ── 4. Run migrations on the fresh database ───────────────────────────────────
info "Running database migrations…"
docker compose run --rm migrate

# ── 5. Start the rest of the stack (backend needs tables to exist for seeding) ─
info "Starting remaining services…"
docker compose up -d --remove-orphans

# ── 5. Summary ────────────────────────────────────────────────────────────────
echo ""
echo -e "${GREEN}${BOLD}══════════════════════════════════════════════${RESET}"
echo -e "${GREEN}${BOLD}  ✓ wallium database and media wiped.          ${RESET}"
echo -e "${GREEN}${BOLD}  ✓ Fresh database migrated and stack running. ${RESET}"
echo -e "${GREEN}${BOLD}══════════════════════════════════════════════${RESET}"
echo ""
info "Container status:"
docker compose ps
echo ""
echo -e " ${CYAN}Useful commands:${RESET}"
echo "   sudo systemctl status wallium                 – service status"
echo "   docker compose -f ${INSTALL_DIR}/docker-compose.yml logs -f  – live logs"
echo ""
