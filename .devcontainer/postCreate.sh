#!/usr/bin/env bash
set -euo pipefail

echo "==> ginbar postCreate: installing user-scope Go tools..."

mkdir -p "$HOME/.local/bin"

# gopls + goimports are version-specific — better installed fresh per-user.
# sqlc, goose, air, golangci-lint are pre-baked in the image.
go install golang.org/x/tools/cmd/goimports@latest
go install golang.org/x/tools/gopls@latest

# pgcli — nicer psql
pip3 install --user pgcli 2>/dev/null || true

echo "==> Go tools done."

# ── Backend deps ──────────────────────────────────────────────────────────────
if [ -f /workspace/src/backend/go.mod ]; then
  echo "==> go mod download (backend)"
  cd /workspace/src/backend && go mod download
fi

# ── Frontend deps ─────────────────────────────────────────────────────────────
if [ -f /workspace/src/frontend/package.json ]; then
  echo "==> pnpm install (frontend)"
  cd /workspace/src/frontend && pnpm install
fi

echo ""
echo "==> postCreate complete."
echo "    pgAdmin  : http://localhost:5050"
echo "    psql     : PGPASSWORD=devpassword psql -h localhost -U ginbar ginbar"
echo "    make dev-backend / make dev-frontend"
