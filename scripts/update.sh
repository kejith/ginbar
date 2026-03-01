#!/usr/bin/env bash
# =============================================================================
# wallium updater
# Pulls latest code, rebuilds changed images, runs migrations, restarts stack.
# Run as root or with sudo.
# =============================================================================
set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'
info()    { echo -e "${CYAN}→${RESET} $*"; }
success() { echo -e "${GREEN}✓${RESET} $*"; }
error()   { echo -e "${RED}✗${RESET} $*" >&2; exit 1; }

[[ "$EUID" -ne 0 ]] && error "Please run as root: sudo bash scripts/update.sh"

# ── Resolve install directory (also accepts legacy GINBAR_DIR / /opt/ginbar) ─
_DEFAULT_DIR="/opt/wallium"
if [[ -z "${WALLIUM_DIR:-}" && -z "${GINBAR_DIR:-}" ]]; then
  # Neither override set — prefer /opt/wallium, fall back to /opt/ginbar
  if [[ ! -d "$_DEFAULT_DIR" && -d "/opt/ginbar" ]]; then
    _DEFAULT_DIR="/opt/ginbar"
    info "Legacy install directory detected at /opt/ginbar"
  fi
fi
INSTALL_DIR="${WALLIUM_DIR:-${GINBAR_DIR:-$_DEFAULT_DIR}}"
[[ -d "$INSTALL_DIR" ]] || error "Install directory not found: $INSTALL_DIR (set \$WALLIUM_DIR to override)"
cd "$INSTALL_DIR"

# Source .env so MEDIA_DIR, FRONTEND_DIR, and LOG_DIR are available
[[ -f "$INSTALL_DIR/.env" ]] && { set -a; source "$INSTALL_DIR/.env"; set +a; }
MEDIA_DIR="${MEDIA_DIR:-${INSTALL_DIR}/media}"
FRONTEND_DIR="${FRONTEND_DIR:-${INSTALL_DIR}/frontend}"
LOG_DIR="${LOG_DIR:-${INSTALL_DIR}/logs}"

echo -e "\n${BOLD}${CYAN}══ wallium update ══${RESET}\n"

# ── 1. Pull latest code ───────────────────────────────────────────────────────
# BRANCH can be overridden via env var; defaults to the feature branch.
BRANCH="${WALLIUM_BRANCH:-master}"

info "Pulling latest code (branch: ${BRANCH})…"
git fetch origin

CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)
if [[ "$CURRENT_BRANCH" != "$BRANCH" ]]; then
  info "Switching from ${CURRENT_BRANCH} to ${BRANCH}…"
  git checkout "$BRANCH"
  success "Switched to branch ${BRANCH}"
fi

LOCAL=$(git rev-parse HEAD)
REMOTE=$(git rev-parse "origin/${BRANCH}")

if [[ "$LOCAL" == "$REMOTE" ]]; then
  echo "  Already up to date ($LOCAL)"
else
  git merge --ff-only "origin/${BRANCH}"
  success "Updated $(git rev-parse --short HEAD~1)..$(git rev-parse --short HEAD)"
  git log --oneline "${LOCAL}..HEAD"
fi

# ── 1b. One-time ginbar → wallium migration ──────────────────────────────────
# Migrate .env: replace any lingering /opt/ginbar paths with the actual
# install dir so subsequent steps (nginx, service) use the right paths.
if grep -q '/opt/ginbar' "$INSTALL_DIR/.env" 2>/dev/null; then
  info "Migrating .env: replacing /opt/ginbar paths with ${INSTALL_DIR}…"
  sed -i "s|/opt/ginbar|${INSTALL_DIR}|g" "$INSTALL_DIR/.env"
  # Re-source with updated values
  set -a; source "$INSTALL_DIR/.env"; set +a
  MEDIA_DIR="${MEDIA_DIR:-${INSTALL_DIR}/media}"
  FRONTEND_DIR="${FRONTEND_DIR:-${INSTALL_DIR}/frontend}"
  success ".env paths updated"
fi

# Migrate nginx vhost: rename ginbar* site files to the current domain name
for OLD_VHOST in /etc/nginx/sites-available/ginbar*; do
  [[ -e "$OLD_VHOST" ]] || continue
  OLD_DOMAIN=$(grep -oP 'server_name \K[^;]+' "$OLD_VHOST" 2>/dev/null | head -1 | xargs || true)
  NEW_VHOST="/etc/nginx/sites-available/${OLD_DOMAIN:-wallium}"
  # Remove stale symlink regardless
  OLD_LINK="/etc/nginx/sites-enabled/$(basename "$OLD_VHOST")"
  [[ -L "$OLD_LINK" ]] && rm -f "$OLD_LINK" && info "Removed stale symlink: $OLD_LINK"
  if [[ "$OLD_VHOST" != "$NEW_VHOST" ]]; then
    info "Renaming nginx vhost: $(basename "$OLD_VHOST") → $(basename "$NEW_VHOST")…"
    mv "$OLD_VHOST" "$NEW_VHOST"
    ln -sf "$NEW_VHOST" "/etc/nginx/sites-enabled/$(basename "$NEW_VHOST")"
    success "Nginx vhost renamed"
  fi
done

# Remove the nginx default site if it is still enabled (it catches all traffic
# and hides the wallium vhost)
if [[ -L /etc/nginx/sites-enabled/default ]]; then
  rm -f /etc/nginx/sites-enabled/default
  success "Removed nginx default site"
fi

# ── 1c. One-time PostgreSQL role/database migration: ginbar → wallium ────────
# The Postgres data volume is initialised once; POSTGRES_USER/DB env vars in
# docker-compose.yml are ignored on subsequent starts.  If the old ginbar role
# still owns the data we must rename it before goose can connect as wallium.
#
# Strategy: try to connect as ginbar via the unix socket (no password needed
# inside the container).  If that succeeds the migration hasn't run yet.
info "Checking for legacy ginbar PostgreSQL role…"
# Make sure postgres is up (it normally is during an update, but be safe)
docker compose up -d postgres 2>/dev/null
for _i in $(seq 1 20); do
  docker compose exec -T postgres pg_isready -q 2>/dev/null && break
  sleep 1
done

if docker compose exec -T postgres psql -U ginbar -d postgres -c "" >/dev/null 2>&1; then
  # ginbar role is still the owner — check whether wallium already exists
  _WALLIUM=$(docker compose exec -T postgres psql -U ginbar -d postgres -tAc \
    "SELECT 1 FROM pg_roles WHERE rolname='wallium';" 2>/dev/null | tr -d '[:space:]' || true)

  if [[ "$_WALLIUM" != "1" ]]; then
    info "Migrating PostgreSQL: creating wallium role and renaming ginbar database…"
    _PG_PASS="${POSTGRES_PASSWORD:?POSTGRES_PASSWORD must be set in .env for this migration}"
    # Create wallium as superuser (we can't rename ginbar while connected as it)
    docker compose exec -T postgres psql -U ginbar -d postgres \
      -c "CREATE ROLE wallium SUPERUSER LOGIN PASSWORD '${_PG_PASS}';"
    docker compose exec -T postgres psql -U ginbar -d postgres \
      -c "ALTER DATABASE ginbar RENAME TO wallium;"
    success "PostgreSQL role and database renamed to wallium"
  else
    # wallium role exists — rename the database if it's still called ginbar
    _GINBAR_DB=$(docker compose exec -T postgres psql -U wallium -d postgres -tAc \
      "SELECT 1 FROM pg_database WHERE datname='ginbar';" 2>/dev/null | tr -d '[:space:]' || true)
    if [[ "$_GINBAR_DB" == "1" ]]; then
      info "Renaming ginbar database to wallium…"
      docker compose exec -T postgres psql -U wallium -d postgres \
        -c "ALTER DATABASE ginbar RENAME TO wallium;"
      success "PostgreSQL database renamed to wallium"
    fi
  fi
else
  # Can't connect as ginbar — either already migrated or postgres not ready
  _WALLIUM=$(docker compose exec -T postgres psql -U wallium -d postgres -tAc \
    "SELECT 1 FROM pg_roles WHERE rolname='wallium';" 2>/dev/null | tr -d '[:space:]' || true)
  if [[ "$_WALLIUM" == "1" ]]; then
    success "PostgreSQL already using wallium role — no migration needed"
  else
    warn "Could not connect to PostgreSQL as ginbar or wallium — skipping DB migration (manual action may be required)"
  fi
fi

# ── 2. Rebuild changed images ─────────────────────────────────────────────────
info "Building images (only rebuilds layers that changed)…"
docker compose build

# ── 2a. Re-extract frontend assets from the fresh build ──────────────────────
info "Updating frontend assets in ${FRONTEND_DIR}…"
mkdir -p "$FRONTEND_DIR"
FE_CONTAINER="wallium_fe_update_$$"
docker build --target frontend-builder -t wallium/fe-build:update "$INSTALL_DIR" -q
docker create --name "$FE_CONTAINER" wallium/fe-build:update /bin/true >/dev/null
rm -rf "${FRONTEND_DIR:?}"/*
docker cp "${FE_CONTAINER}:/app/dist/." "$FRONTEND_DIR/"
docker rm "$FE_CONTAINER" >/dev/null
docker rmi wallium/fe-build:update >/dev/null
# Ensure www-data (nginx worker) can traverse the install dir and read assets
chmod o+x "$INSTALL_DIR"
chmod -R o+rX "$FRONTEND_DIR"
success "Frontend assets updated at $FRONTEND_DIR"

# ── 3. Run any new migrations ────────────────────────────────────────────────
info "Running database migrations…"
docker compose run --rm migrate

# ── 3b. Ensure log directory exists and logrotate config is current ──────────
info "Ensuring log directory exists at ${LOG_DIR}…"
mkdir -p "$LOG_DIR"
chmod 755 "$LOG_DIR"

LOGROTATE_SRC="${INSTALL_DIR}/scripts/wallium.logrotate"
LOGROTATE_DEST="/etc/logrotate.d/wallium"
if [[ -f "$LOGROTATE_SRC" ]]; then
  sed "s|/opt/wallium/logs|${LOG_DIR}|g" "$LOGROTATE_SRC" > "$LOGROTATE_DEST"
  chmod 644 "$LOGROTATE_DEST"
  success "logrotate config updated at $LOGROTATE_DEST"
fi

# ── 4. Always overwrite the systemd service file and reload ──────────────────
SERVICE_DEST="/etc/systemd/system/wallium.service"
info "Installing systemd service unit…"
sed "s|WorkingDirectory=.*|WorkingDirectory=${INSTALL_DIR}|" \
  "$INSTALL_DIR/wallium.service" > "$SERVICE_DEST"
chmod 644 "$SERVICE_DEST"

# One-time migration: stop and disable the old ginbar.service if it exists
if [[ -f "/etc/systemd/system/ginbar.service" ]]; then
  info "Disabling legacy ginbar.service…"
  systemctl stop ginbar.service 2>/dev/null || true
  systemctl disable ginbar.service 2>/dev/null || true
  rm -f "/etc/systemd/system/ginbar.service"
  success "ginbar.service removed"
fi

systemctl daemon-reload
systemctl enable wallium.service
success "wallium.service installed and daemon reloaded"

# ── 5. Always overwrite the host nginx vhost and reload ──────────────────────
DOMAIN=""
# Accept both wallium* and any remaining ginbar* site files
VHOST_DEST=$(ls /etc/nginx/sites-available/wallium* /etc/nginx/sites-available/ginbar* 2>/dev/null | head -1 || true)
if [[ -n "$VHOST_DEST" ]]; then
  DOMAIN=$(grep -oP 'server_name \K[^;]+' "$VHOST_DEST" | head -1 | xargs)
  CERT_DIR_VHOST="/etc/nginx/certs/${DOMAIN}"
  VHOST_SRC="${INSTALL_DIR}/nginx/wallium.vhost.conf"
  info "Installing nginx vhost for ${DOMAIN}…"
  sed \
    -e "s|wallium\.kejith\.de|${DOMAIN}|g" \
    -e "s|/etc/nginx/certs/wallium|${CERT_DIR_VHOST}|g" \
    -e "s|/opt/wallium/media|${MEDIA_DIR}|g" \
    -e "s|/opt/wallium/frontend|${FRONTEND_DIR}|g" \
    "$VHOST_SRC" > "$VHOST_DEST"
  nginx -t && systemctl reload nginx
  success "Host nginx vhost updated and reloaded"
else
  info "No nginx vhost found in /etc/nginx/sites-available — skipping"
fi

# ── 6. Start / restart the service via systemd ───────────────────────────────
info "Restarting wallium service…"
if systemctl is-active --quiet wallium.service; then
  systemctl restart wallium.service
elif systemctl is-active --quiet ginbar.service; then
  # Legacy service still running — stop it, start the new one
  systemctl stop ginbar.service || true
  systemctl start wallium.service
else
  systemctl start wallium.service
fi
success "wallium.service started"

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
