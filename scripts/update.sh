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

# Source .env so MEDIA_DIR and FRONTEND_DIR are available
[[ -f "$INSTALL_DIR/.env" ]] && { set -a; source "$INSTALL_DIR/.env"; set +a; }
MEDIA_DIR="${MEDIA_DIR:-${INSTALL_DIR}/media}"
FRONTEND_DIR="${FRONTEND_DIR:-${INSTALL_DIR}/frontend}"

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

# ── 2a. Re-extract frontend assets from the fresh build ──────────────────────
info "Updating frontend assets in ${FRONTEND_DIR}…"
mkdir -p "$FRONTEND_DIR"
FE_CONTAINER="ginbar_fe_update_$$"
docker build --target frontend-builder -t ginbar/fe-build:update "$INSTALL_DIR" -q
docker create --name "$FE_CONTAINER" ginbar/fe-build:update /bin/true >/dev/null
rm -rf "${FRONTEND_DIR:?}"/*
docker cp "${FE_CONTAINER}:/app/dist/." "$FRONTEND_DIR/"
docker rm "$FE_CONTAINER" >/dev/null
docker rmi ginbar/fe-build:update >/dev/null
# Ensure www-data (nginx worker) can traverse the install dir and read assets
chmod o+x "$INSTALL_DIR"
chmod -R o+rX "$FRONTEND_DIR"
success "Frontend assets updated at $FRONTEND_DIR"

# ── 3. Run any new migrations ────────────────────────────────────────────────
info "Running database migrations…"
docker compose run --rm migrate

# ── 4. Always overwrite the systemd service file and reload ──────────────────
SERVICE_DEST="/etc/systemd/system/ginbar.service"
info "Installing systemd service unit…"
sed "s|WorkingDirectory=.*|WorkingDirectory=${INSTALL_DIR}|" \
  "$INSTALL_DIR/ginbar.service" > "$SERVICE_DEST"
chmod 644 "$SERVICE_DEST"
systemctl daemon-reload
success "ginbar.service installed and daemon reloaded"

# ── 5. Always overwrite the host nginx vhost and reload ──────────────────────
DOMAIN=""
VHOST_DEST=$(ls /etc/nginx/sites-available/ginbar* 2>/dev/null | head -1 || true)
if [[ -n "$VHOST_DEST" ]]; then
  DOMAIN=$(grep -oP 'server_name \K[^;]+' "$VHOST_DEST" | head -1 | xargs)
  CERT_DIR_VHOST="/etc/nginx/certs/${DOMAIN}"
  VHOST_SRC="${INSTALL_DIR}/nginx/ginbar.vhost.conf"
  info "Installing nginx vhost for ${DOMAIN}…"
  sed \
    -e "s|ginbar\.kejith\.de|${DOMAIN}|g" \
    -e "s|/etc/nginx/certs/ginbar|${CERT_DIR_VHOST}|g" \
    -e "s|/opt/ginbar/media|${MEDIA_DIR}|g" \
    -e "s|/opt/ginbar/frontend|${FRONTEND_DIR}|g" \
    "$VHOST_SRC" > "$VHOST_DEST"
  nginx -t && systemctl reload nginx
  success "Host nginx vhost updated and reloaded"
else
  info "No nginx vhost found in /etc/nginx/sites-available — skipping"
fi

# ── 6. Start / restart the service via systemd ───────────────────────────────
info "Restarting ginbar service…"
if systemctl is-active --quiet ginbar.service; then
  systemctl restart ginbar.service
else
  systemctl start ginbar.service
fi
success "ginbar.service started"

# ── 7. Health check ───────────────────────────────────────────────────────────
sleep 5
info "Container status:"
docker compose ps

echo ""

if [[ -n "$DOMAIN" ]]; then
  HTTP_CODE=$(curl -sk -o /dev/null -w "%{http_code}" "https://${DOMAIN}/api/check/me" || true)
  if [[ "$HTTP_CODE" =~ ^2 ]]; then
    echo -e "\n${GREEN}${BOLD}✓ https://${DOMAIN} is healthy (HTTP ${HTTP_CODE})${RESET}\n"
  else
    echo -e "\n${RED}! https://${DOMAIN}/api/check/me returned HTTP ${HTTP_CODE}${RESET}"
    echo "  Check logs: docker compose -f ${INSTALL_DIR}/docker-compose.yml logs -f"
  fi
fi
