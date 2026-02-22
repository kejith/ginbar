#!/usr/bin/env bash
# =============================================================================
# wallium installer
# Walks through deploying the full stack on a fresh server.
# Run as root or with sudo.
# =============================================================================
set -euo pipefail

# ── Colours ──────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'

info()    { echo -e "${CYAN}→${RESET} $*"; }
success() { echo -e "${GREEN}✓${RESET} $*"; }
warn()    { echo -e "${YELLOW}!${RESET} $*"; }
error()   { echo -e "${RED}✗${RESET} $*" >&2; }
step()    { echo -e "\n${BOLD}${CYAN}══ $* ══${RESET}"; }
pause()   { echo -e "${YELLOW}Press Enter to continue…${RESET}"; read -r; }

# ── Root check ───────────────────────────────────────────────────────────────
if [[ "$EUID" -ne 0 ]]; then
  error "Please run as root: sudo bash scripts/install.sh"
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(dirname "$SCRIPT_DIR")"

# ── Welcome ──────────────────────────────────────────────────────────────────
clear
echo -e "${BOLD}"
echo "  ██     ██   █████   ██      ██       ████   ██   ██  ███    ███ "
echo "  ██     ██  ██   ██  ██      ██        ██    ██   ██  ████  ████ "
echo "  ██  █  ██  ███████  ██      ██        ██    ██   ██  ██ ████ ██ "
echo "  ██ ███ ██  ██   ██  ██      ██        ██    ██   ██  ██  ██  ██ "
echo "   ███ ███   ██   ██  ██████  ██████   ████    █████   ██      ██ "
echo -e "${RESET}"
echo -e " ${CYAN}Production Installer${RESET}"
echo " ─────────────────────────────────────────────"
echo " This script will guide you through deploying"
echo " wallium on your server behind Cloudflare."
echo ""
pause

# ── Step 1: Prerequisites ─────────────────────────────────────────────────────
step "1 / 8  Checking prerequisites"

check_cmd() {
  if command -v "$1" &>/dev/null; then
    success "$1 found ($(command -v "$1"))"
  else
    warn "$1 not found — installing…"
    apt-get update -qq && apt-get install -y -qq "$2"
    success "$1 installed"
  fi
}

check_cmd git    git
check_cmd nginx  nginx
check_cmd curl   curl
check_cmd openssl openssl

# Docker
if ! command -v docker &>/dev/null; then
  warn "Docker not found — installing via get.docker.com…"
  curl -fsSL https://get.docker.com | sh
  success "Docker installed"
else
  success "Docker found ($(docker --version))"
fi

# docker compose plugin
if ! docker compose version &>/dev/null; then
  error "docker compose plugin not available. Install Docker Engine >= 23 and try again."
  exit 1
fi
success "docker compose plugin available"

# ── Step 2: Installation directory ───────────────────────────────────────────
step "2 / 8  Installation directory"

DEFAULT_INSTALL_DIR="/opt/wallium"
read -rp "Install directory [${DEFAULT_INSTALL_DIR}]: " INSTALL_DIR
INSTALL_DIR="${INSTALL_DIR:-$DEFAULT_INSTALL_DIR}"

if [[ "$REPO_DIR" == "$INSTALL_DIR" ]]; then
  success "Already running from install directory: $INSTALL_DIR"
else
  if [[ -d "$INSTALL_DIR/.git" ]]; then
    info "Existing repo found at $INSTALL_DIR — pulling latest…"
    git -C "$INSTALL_DIR" pull
  else
    read -rp "Git repository URL (leave blank to copy current directory): " REPO_URL
    if [[ -n "$REPO_URL" ]]; then
      git clone "$REPO_URL" "$INSTALL_DIR"
    else
      info "Copying files to $INSTALL_DIR…"
      mkdir -p "$INSTALL_DIR"
      rsync -a --exclude='.git' --exclude='src/backend/tmp/' \
            --exclude='src/frontend/node_modules/' \
            --exclude='src/frontend/dist/' \
            "$REPO_DIR/" "$INSTALL_DIR/"
    fi
  fi
  success "Files ready at $INSTALL_DIR"
fi

cd "$INSTALL_DIR"

# ── Step 3: Domain ───────────────────────────────────────────────────────────
step "3 / 8  Domain configuration"

DEFAULT_DOMAIN="wallium.kejith.de"
read -rp "Domain name [${DEFAULT_DOMAIN}]: " DOMAIN
DOMAIN="${DOMAIN:-$DEFAULT_DOMAIN}"
success "Domain: $DOMAIN"

echo ""
echo -e " ${YELLOW}Cloudflare DNS — required before continuing:${RESET}"
echo "   Add an A record in your Cloudflare dashboard:"
echo ""
echo -e "   Type:    ${BOLD}A${RESET}"
echo -e "   Name:    ${BOLD}${DOMAIN%%.*}${RESET}  (subdomain part)"
echo -e "   Content: ${BOLD}$(curl -s4 ifconfig.me 2>/dev/null || echo '<this server public IP>')${RESET}"
echo -e "   Proxy:   ${BOLD}Proxied (orange cloud)${RESET}"
echo ""
pause

# ── Step 4: Cloudflare Origin Certificate ────────────────────────────────────
step "4 / 8  Cloudflare Origin Certificate (SSL)"

CERT_DIR="/etc/nginx/certs/${DOMAIN}"
mkdir -p "$CERT_DIR"

echo ""
echo -e " ${YELLOW}Generate a Cloudflare Origin Certificate:${RESET}"
echo "   1. Go to Cloudflare Dashboard → your domain"
echo "   2. SSL/TLS → Origin Server → Create Certificate"
echo "   3. Add hostname: ${DOMAIN}"
echo "   4. Validity: 15 years (recommended)"
echo "   5. Download both files"
echo ""

CERT_FILE="${CERT_DIR}/origin.crt"
KEY_FILE="${CERT_DIR}/origin.key"

if [[ -f "$CERT_FILE" && -f "$KEY_FILE" ]]; then
  success "Cert files already present at $CERT_DIR — skipping."
else
  # Check if certs are in the repo
  REPO_CERT="${INSTALL_DIR}/nginx/certs/origin.crt"
  REPO_KEY="${INSTALL_DIR}/nginx/certs/origin.key"

  if [[ -f "$REPO_CERT" && -f "$REPO_KEY" ]]; then
    info "Found certs in repo directory — copying to $CERT_DIR…"
    cp "$REPO_CERT" "$CERT_FILE"
    cp "$REPO_KEY"  "$KEY_FILE"
    success "Certs copied from repo"
  else
    echo -e " Paste the ${BOLD}certificate${RESET} content (origin.crt) below."
    echo " Press Enter, paste, then type END on a new line and press Enter:"
    echo ""
    CERT_CONTENT=""
    while IFS= read -r line; do
      [[ "$line" == "END" ]] && break
      CERT_CONTENT+="$line"$'\n'
    done
    echo "$CERT_CONTENT" > "$CERT_FILE"

    echo ""
    echo -e " Paste the ${BOLD}private key${RESET} content (origin.key) below."
    echo " Press Enter, paste, then type END on a new line and press Enter:"
    echo ""
    KEY_CONTENT=""
    while IFS= read -r line; do
      [[ "$line" == "END" ]] && break
      KEY_CONTENT+="$line"$'\n'
    done
    echo "$KEY_CONTENT" > "$KEY_FILE"
  fi
fi

chmod 600 "$KEY_FILE"
chmod 644 "$CERT_FILE"
success "Certificate: $CERT_FILE"
success "Private key: $KEY_FILE (mode 600)"

# ── Step 5: Environment variables ────────────────────────────────────────────
step "5 / 8  Environment variables"

ENV_FILE="${INSTALL_DIR}/.env"

if [[ -f "$ENV_FILE" ]]; then
  warn ".env already exists at $ENV_FILE"
  read -rp "Overwrite? [y/N]: " OVERWRITE_ENV
  [[ "${OVERWRITE_ENV,,}" != "y" ]] && { success "Keeping existing .env"; } || {
    generate_env=true
  }
else
  generate_env=true
fi

if [[ "${generate_env:-false}" == "true" ]]; then
  echo ""
  read -rsp "PostgreSQL password (leave blank to auto-generate): " PG_PASS
  echo ""
  [[ -z "$PG_PASS" ]] && PG_PASS="$(openssl rand -hex 32)" && info "Generated POSTGRES_PASSWORD"

  read -rsp "Session secret (leave blank to auto-generate): " SESSION_SECRET
  echo ""
  [[ -z "$SESSION_SECRET" ]] && SESSION_SECRET="$(openssl rand -hex 32)" && info "Generated SESSION_SECRET"

  MEDIA_DIR="${INSTALL_DIR}/media"
  FRONTEND_DIR="${INSTALL_DIR}/frontend"

  cat > "$ENV_FILE" <<EOF
POSTGRES_PASSWORD=${PG_PASS}
SESSION_SECRET=${SESSION_SECRET}
# Paths used by docker-compose and host nginx
MEDIA_DIR=${MEDIA_DIR}
FRONTEND_DIR=${FRONTEND_DIR}
# Optional overrides:
# POSTGRES_DB=wallium
# POSTGRES_USER=wallium
EOF
  chmod 600 "$ENV_FILE"
  success ".env written to $ENV_FILE"
fi

# ── Step 6: Host nginx vhost ──────────────────────────────────────────────────
step "6 / 8  Host nginx vhost"

VHOST_SRC="${INSTALL_DIR}/nginx/wallium.vhost.conf"
VHOST_DEST="/etc/nginx/sites-available/${DOMAIN}"
VHOST_LINK="/etc/nginx/sites-enabled/${DOMAIN}"

# Read paths from .env (or fall back to INSTALL_DIR defaults)
[[ -f "$ENV_FILE" ]] && { set -a; source "$ENV_FILE"; set +a; }
MEDIA_DIR="${MEDIA_DIR:-${INSTALL_DIR}/media}"
FRONTEND_DIR="${FRONTEND_DIR:-${INSTALL_DIR}/frontend}"

# Patch domain, cert paths, media dir, and frontend dir into vhost template
sed \
  -e "s|wallium\.kejith\.de|${DOMAIN}|g" \
  -e "s|/etc/nginx/certs/wallium|${CERT_DIR}|g" \
  -e "s|/opt/wallium/media|${MEDIA_DIR}|g" \
  -e "s|/opt/wallium/frontend|${FRONTEND_DIR}|g" \
  "$VHOST_SRC" > "$VHOST_DEST"

ln -sf "$VHOST_DEST" "$VHOST_LINK"
success "Vhost installed: $VHOST_DEST"

nginx -t
systemctl reload nginx
success "Host nginx reloaded"

# ── Step 7: Build & start stack ──────────────────────────────────────────────
step "7 / 8  Build and start Docker stack"

cd "$INSTALL_DIR"

info "Building Docker images (this may take a few minutes)…"
docker compose build

# ── Extract compiled frontend from the build image ────────────────────────
info "Extracting frontend assets to ${FRONTEND_DIR}…"
mkdir -p "$FRONTEND_DIR"
FE_CONTAINER="wallium_fe_extract_$$"
docker build --target frontend-builder -t wallium/fe-build:extract "$INSTALL_DIR" -q
docker create --name "$FE_CONTAINER" wallium/fe-build:extract /bin/true >/dev/null
docker cp "${FE_CONTAINER}:/app/dist/." "$FRONTEND_DIR/"
docker rm "$FE_CONTAINER" >/dev/null
docker rmi wallium/fe-build:extract >/dev/null
success "Frontend assets extracted to $FRONTEND_DIR"

# ── Create media directories and ensure nginx can read them ───────────────
info "Creating media directories at ${MEDIA_DIR}…"
mkdir -p \
  "${MEDIA_DIR}/images/thumbnails" \
  "${MEDIA_DIR}/videos" \
  "${MEDIA_DIR}/upload"
# Allow the Docker backend user (uid 0 in debian:slim) and nginx (www-data) to
# read and traverse.  o+x on the parent dir is critical — without it www-data
# cannot stat files inside even if the files themselves are world-readable.
chmod o+x "$INSTALL_DIR"
chmod -R 755 "$MEDIA_DIR"
chmod -R o+rX "$FRONTEND_DIR"
success "Media directories ready at $MEDIA_DIR"

info "Running database migrations…"
docker compose run --rm migrate

info "Starting all services…"
docker compose up -d

success "Docker stack started"

# ── Step 8: systemd service ───────────────────────────────────────────────────
step "8 / 8  systemd service (auto-start on reboot)"

SERVICE_SRC="${INSTALL_DIR}/wallium.service"
SERVICE_DEST="/etc/systemd/system/wallium.service"

# Patch WorkingDirectory in the service file
sed "s|WorkingDirectory=.*|WorkingDirectory=${INSTALL_DIR}|" \
  "$SERVICE_SRC" > "$SERVICE_DEST"

chmod 644 "$SERVICE_DEST"
systemctl daemon-reload
systemctl enable wallium.service
success "wallium.service enabled (starts on boot)"

# ── Health check ─────────────────────────────────────────────────────────────
echo ""
step "Health check"

sleep 5

info "Docker container status:"
docker compose ps

echo ""
HEALTH=$(curl -sk "https://${DOMAIN}/api/check/me" -w "\nHTTP %{http_code}" 2>&1 || true)
echo "$HEALTH"

if echo "$HEALTH" | grep -qE "HTTP 20[0-9]"; then
  echo ""
  echo -e "${GREEN}${BOLD}══════════════════════════════════════════${RESET}"
  echo -e "${GREEN}${BOLD}  ✓ wallium is live at https://${DOMAIN}${RESET}"
  echo -e "${GREEN}${BOLD}══════════════════════════════════════════${RESET}"
else
  warn "HTTPS check didn't return 2xx — this is normal if Cloudflare DNS is still propagating."
  echo ""
  echo " Try manually:"
  echo "   curl -sk https://${DOMAIN}/api/check/me"
  echo "   docker compose -f ${INSTALL_DIR}/docker-compose.yml logs"
fi

echo ""
echo -e " ${CYAN}Useful commands:${RESET}"
echo "   sudo systemctl status wallium       – service status"
echo "   sudo systemctl restart wallium      – restart stack"
echo "   docker compose -f ${INSTALL_DIR}/docker-compose.yml logs -f  – live logs"
echo ""
