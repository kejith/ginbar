#!/usr/bin/env bash
set -euo pipefail

echo "==> wallium postCreate: installing user-scope Go tools..."

mkdir -p "$HOME/.local/bin"

# gopls + goimports are version-specific — better installed fresh per-user.
# sqlc, goose, air, golangci-lint are pre-baked in the image.
# Pin to the last release that supports Go 1.24 (gopls v0.21+ requires Go 1.25).
go install golang.org/x/tools/cmd/goimports@v0.30.0
go install golang.org/x/tools/gopls@v0.20.0

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
echo "    psql     : PGPASSWORD=devpassword psql -h localhost -U wallium wallium"
echo "    make dev-backend / make dev-frontend"
