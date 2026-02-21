#!/usr/bin/env bash
# =============================================================================
# ginbar updater
# Pulls latest code, rebuilds changed images, runs migrations, restarts stack.
# Run as root or with sudo.
# =============================================================================
set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'
info()    { echo -e "${CYAN}→${RESET} $*"; }
success() { echo -e "${GREEN}✓${RESET} $*"; }
error()   { echo -e "${RED}✗${RESET} $*" >&2; exit 1; }

[[ "$EUID" -ne 0 ]] && error "Please run as root: sudo bash scripts/update.sh"

INSTALL_DIR="${GINBAR_DIR:-/opt/ginbar}"
[[ -d "$INSTALL_DIR" ]] || error "Install directory not found: $INSTALL_DIR (set \$GINBAR_DIR to override)"
cd "$INSTALL_DIR"

echo -e "\n${BOLD}${CYAN}══ ginbar update ══${RESET}\n"

# ── 1. Pull latest code ───────────────────────────────────────────────────────
info "Pulling latest code…"
git fetch origin
LOCAL=$(git rev-parse HEAD)
REMOTE=$(git rev-parse @{u})

if [[ "$LOCAL" == "$REMOTE" ]]; then
  echo "  Already up to date ($LOCAL)"
else
  git pull --ff-only
  success "Updated $(git rev-parse --short HEAD~1)..$(git rev-parse --short HEAD)"
  git log --oneline "${LOCAL}..HEAD"
fi

# ── 2. Rebuild changed images ─────────────────────────────────────────────────
info "Building images (only rebuilds layers that changed)…"
docker compose build

# ── 3. Run any new migrations ────────────────────────────────────────────────
info "Running database migrations…"
docker compose run --rm migrate

# ── 4. Restart the stack with new images ─────────────────────────────────────
info "Restarting stack…"
docker compose up -d --remove-orphans

# ── 5. Reload systemd unit if the service file changed ───────────────────────
SERVICE_DEST="/etc/systemd/system/ginbar.service"
if ! diff -q "$INSTALL_DIR/ginbar.service" "$SERVICE_DEST" &>/dev/null; then
  info "ginbar.service changed — reloading systemd…"
  sed "s|WorkingDirectory=.*|WorkingDirectory=${INSTALL_DIR}|" \
    "$INSTALL_DIR/ginbar.service" > "$SERVICE_DEST"
  systemctl daemon-reload
  success "systemd unit reloaded"
fi

# ── 6. Reload host nginx vhost if it changed ─────────────────────────────────
VHOST_DEST=$(ls /etc/nginx/sites-available/ginbar* 2>/dev/null | head -1 || true)
if [[ -n "$VHOST_DEST" ]]; then
  DOMAIN=$(grep -oP 'server_name \K[^;]+' "$VHOST_DEST" | head -1 | xargs)
  VHOST_SRC="${INSTALL_DIR}/nginx/ginbar.vhost.conf"
  TMP_VHOST=$(mktemp)
  sed \
    -e "s|ginbar\.kejith\.de|${DOMAIN}|g" \
    -e "s|/etc/nginx/certs/ginbar|/etc/nginx/certs/${DOMAIN}|g" \
    "$VHOST_SRC" > "$TMP_VHOST"
  if ! diff -q "$TMP_VHOST" "$VHOST_DEST" &>/dev/null; then
    info "Nginx vhost changed — reloading…"
    cp "$TMP_VHOST" "$VHOST_DEST"
    nginx -t && systemctl reload nginx
    success "Host nginx reloaded"
  fi
  rm -f "$TMP_VHOST"
fi

# ── 7. Health check ───────────────────────────────────────────────────────────
sleep 3
info "Container status:"
docker compose ps

echo ""
DOMAIN="${DOMAIN:-}"
if [[ -z "$DOMAIN" && -f /etc/nginx/sites-enabled/* ]]; then
  DOMAIN=$(grep -oP 'server_name \K[^;]+' /etc/nginx/sites-enabled/* 2>/dev/null | head -1 | xargs || true)
fi

if [[ -n "$DOMAIN" ]]; then
  HTTP_CODE=$(curl -sk -o /dev/null -w "%{http_code}" "https://${DOMAIN}/api/check/me" || true)
  if [[ "$HTTP_CODE" =~ ^2 ]]; then
    echo -e "\n${GREEN}${BOLD}✓ https://${DOMAIN} is healthy (HTTP ${HTTP_CODE})${RESET}\n"
  else
    echo -e "\n${RED}! https://${DOMAIN}/api/check/me returned HTTP ${HTTP_CODE}${RESET}"
    echo "  Check logs: docker compose -f ${INSTALL_DIR}/docker-compose.yml logs -f"
  fi
fi
