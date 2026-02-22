#!/usr/bin/env bash
# Runs every time the container starts (not just first create).
set -euo pipefail

# ── SSH keys ─────────────────────────────────────────────────────────────────
# Copy host SSH keys (mounted read-only at /home/vscode/.ssh-host) into the
# writable ~/.ssh directory so the SSH client can use them with correct perms.
if [ -d /home/vscode/.ssh-host ]; then
  mkdir -p ~/.ssh
  cp -n /home/vscode/.ssh-host/* ~/.ssh/ 2>/dev/null || true
  chmod 700 ~/.ssh
  chmod 600 ~/.ssh/id_* ~/.ssh/config 2>/dev/null || true
  chmod 644 ~/.ssh/*.pub ~/.ssh/known_hosts 2>/dev/null || true
fi

# Nothing blocking here — just print connection info
echo "┌─────────────────────────────────────────────────────────────┐"
echo "│  Wallium Dev Container                                        │"
echo "│  PostgreSQL : localhost:5432  db=wallium  user=wallium         │"
echo "│  pgAdmin    : http://localhost:5050                          │"
echo "│  Backend    : air (in src/backend) → http://localhost:3000   │"
echo "│  Frontend   : pnpm dev (in src/frontend) → http://localhost:5173 │"
echo "└─────────────────────────────────────────────────────────────┘"
