## wallium — root Makefile
# Targets delegate into src/ sub-projects.
# Run from repo root inside devcontainer.

.PHONY: dev dev-build dev-build-backend dev-build-worker dev-backend dev-frontend migrate sqlc lint build up down logs migrate-prod dev-clean test test-integration test-all test-e2e bench-e2e e2e-run e2e-bench-run

# ── Dev ───────────────────────────────────────────────────────────────────────

## dev: migrate + start backend (air) + frontend (vite) — Ctrl-C stops all
dev:
	@bash dev.sh

## dev-build: compile Go backend + Rust worker binaries for local dev
dev-build: dev-build-backend dev-build-worker
	@echo "✓ dev binaries ready"

dev-build-backend:
	cd src/backend && go build -o wallium-backend .

dev-build-worker:
	cd src/worker && cargo build

dev-backend:
	cd src/backend && air

dev-frontend:
	cd src/frontend && pnpm dev

# ── Logging ───────────────────────────────────────────────────────────────────
## logs-tail: pretty-print the production JSON log (requires jq)
logs-tail:
	tail -f $${LOG_FILE:-/opt/wallium/logs/app.log} | jq .

## logs-errors: stream only warn/error lines from the production log
logs-errors:
	tail -f $${LOG_FILE:-/opt/wallium/logs/app.log} | jq 'select(.level=="WARN" or .level=="ERROR")'

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

## test: run worker unit tests (10-image testset sample; set WALLIUM_TESTSET_SIZE=100 for full corpus)
test:
	cd src/worker && cargo test -p wallium-worker

## test-integration: run DB/Redis integration tests (requires Postgres + Redis)
test-integration:
	cd src/worker && cargo test -p wallium-worker -- --ignored --test-threads=1

## test-all: unit + integration tests
test-all: test test-integration

## test-data: download 100 pr0gramm testset images into src/worker/test_data/images/ (idempotent)
test-data:
	go run ./scripts/download_testset.go

psql:
	PGPASSWORD=devpassword psql -h localhost -U wallium wallium
