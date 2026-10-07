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


## First admin bootstrap

After creating the first user on a fresh installation, establish the first admin through the local operational command:

```bash
DATABASE_URL='postgres://...' go run ./cmd/bootstrap-admin --user-id <numeric-user-id>
```

The bootstrap command talks directly to PostgreSQL; there is no unauthenticated HTTP bootstrap endpoint. It is one-shot: after any admin exists, further bootstrap attempts are rejected. The initial role records the bootstrapped numeric user ID as its grant provenance.

After bootstrap, authenticated admins may grant or revoke admin status with `PUT` or `DELETE /api/v2/admin/users/{id}/roles/admin`. Admin self-revocation is intentionally rejected so a successful revocation always leaves the acting admin in place.
