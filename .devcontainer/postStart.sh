#!/usr/bin/env bash
# Runs every time the container starts (not just first create).
set -euo pipefail

# Nothing blocking here — just print connection info
echo "┌─────────────────────────────────────────────────────────────┐"
echo "│  Wallium Dev Container                                        │"
echo "│  PostgreSQL : localhost:5432  db=wallium  user=wallium         │"
echo "│  pgAdmin    : http://localhost:5050                          │"
echo "│  Backend    : air (in src/backend) → http://localhost:3000   │"
echo "│  Frontend   : pnpm dev (in src/frontend) → http://localhost:5173 │"
echo "└─────────────────────────────────────────────────────────────┘"
