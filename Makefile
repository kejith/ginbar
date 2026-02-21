## ginbar — root Makefile
# Targets delegate into src/ sub-projects.
# Run from repo root inside devcontainer.

.PHONY: dev dev-backend dev-frontend migrate sqlc lint build up down logs migrate-prod

# ── Dev ───────────────────────────────────────────────────────────────────────

## dev: migrate + start backend (air) + frontend (vite) — Ctrl-C stops all
dev:
	@bash dev.sh

dev-backend:
	cd src/backend && air

dev-frontend:
	cd src/frontend && pnpm dev

# ── Database ───────────────────────────────────────────────────────────────────
PG_URL ?= postgres://ginbar:devpassword@localhost:5432/ginbar?sslmode=disable

migrate-up:
	goose -dir src/backend/db/migrations postgres "$(PG_URL)" up

migrate-down:
	goose -dir src/backend/db/migrations postgres "$(PG_URL)" down

migrate-status:
	goose -dir src/backend/db/migrations postgres "$(PG_URL)" status

# ── Code generation ────────────────────────────────────────────────────────────
sqlc:
	cd src/backend && sqlc generate

# ── Quality ───────────────────────────────────────────────────────────────────
lint-backend:
	cd src/backend && golangci-lint run ./...

lint-frontend:
	cd src/frontend && pnpm lint

# ── Build (prod) ──────────────────────────────────────────────────────────────
build:
	docker compose build

up:
	docker compose up -d

down:
	docker compose down

logs:
	docker compose logs -f

migrate-prod:
	docker compose run --rm migrate

# ── Convenience ───────────────────────────────────────────────────────────────
psql:
	PGPASSWORD=devpassword psql -h localhost -U ginbar ginbar
