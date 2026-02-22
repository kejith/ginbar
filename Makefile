## wallium — root Makefile
# Targets delegate into src/ sub-projects.
# Run from repo root inside devcontainer.

.PHONY: dev dev-backend dev-frontend migrate sqlc lint build up down logs migrate-prod dev-clean

# ── Dev ───────────────────────────────────────────────────────────────────────

## dev: migrate + start backend (air) + frontend (vite) — Ctrl-C stops all
dev:
	@bash dev.sh

dev-backend:
	cd src/backend && air

dev-frontend:
	cd src/frontend && pnpm dev

# ── Database ───────────────────────────────────────────────────────────────────
PG_URL ?= postgres://wallium:devpassword@localhost:5432/wallium?sslmode=disable

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

## dev-clean: wipe dev database, media files, and tmp — then re-run migrations
dev-clean:
	@bash scripts/dev-clean.sh

psql:
	PGPASSWORD=devpassword psql -h localhost -U wallium wallium
