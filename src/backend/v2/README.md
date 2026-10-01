# Ginbar v2 backend

Clean-slate Go/PostgreSQL backend for Ginbar v2. The legacy `src/backend` tree remains reference-only while this module is built out.

## First M2 slice

- fresh PostgreSQL core schema in `internal/schema/migrations/001_core.sql`
- immutable numeric IDs and identity/credential separation
- released-post ID cursor query; no OFFSET pagination
- around-post query for deep links
- real search lexer/parser/AST for include/exclude tags and `score` predicates
- strict JSON feed/around-post HTTP contracts using `net/http`
- PostgreSQL adapter isolated behind the feed store interface
- initial pool cap defaults to 8 connections

## Commands

```bash
go test ./...
go run ./cmd/api
```

Runtime variables:

- `DATABASE_URL` (required)
- `LISTEN_ADDR` (default `:8080`)
- `DB_MAX_CONNS` (default `8`)

The first server benchmark should populate realistic released posts/tags, then capture `EXPLAIN (ANALYZE, BUFFERS)` for the feed, tag search, and around-post shapes before changing indexes.
