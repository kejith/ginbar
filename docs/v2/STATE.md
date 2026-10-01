# Ginbar v2 State / Handoff

Last updated: 2026-10-01
Phase: M2 core schema/API first slice implemented; target-server PostgreSQL benchmark gate outstanding
Integration branch: `v2`
Active implementation branch: `astra/m2-core-schema-api`
Completed M1 branch: `astra/m1-scroll-anchor`
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the minimal resume point for humans and Astra. Read it before `PLAN.md`. Do not use chat history as project memory.

## Branch status

- `master` remains untouched by rewrite work.
- M1 is complete and integrated into `v2` at `32e154c4541fcdd2c24c1c05ed278e2dbddef960`.
- `astra/m2-core-schema-api` was created directly from that exact `v2` head.
- The M2 branch is currently a clean descendant of `v2`; do not merge it until the real Go/PostgreSQL/server gate below passes.
- M2 commits so far:
  - `7c3b6b2ac1599b5cb1b04e9a73722197011c9805` — search/feed/domain/HTTP service contracts
  - `caee8b57455be9ea460594896d6e828fa3b1d2db` — PostgreSQL schema, query compiler/store, API executable, benchmark fixtures
  - `b5f69dee11048d9ff7a3f9bde15926f125200b9c` — M2 docs/runbook and workspace switch

## M1 decision retained

- SolidJS is accepted for the v2 frontend.
- Keep incremental retention + CSS containment; do not add virtualization without later evidence.
- Frontend/browser timings belong to the client machine running the browser.
- Target-server benchmarking is authoritative only for backend/server-side implementation.

## M2 architecture in this slice

M2 is a clean v2 backend module at `src/backend/v2`. The legacy `src/backend` / `wallium` implementation is reference-only and is not extended.

The root Go workspace now points only at `src/backend/v2`; the stale legacy workspace sum was removed.

Current backend choices:

- Go 1.25 minimum
- direct `pgx/v5` PostgreSQL interface, currently pinned to 5.11.0
- standard-library `net/http`; no application web framework
- PostgreSQL authoritative for application state and durable media jobs
- no Redis dependency yet; add it only for a measured ephemeral responsibility
- initial PostgreSQL pool cap: 8 connections
- 3-second application/request context for current feed DB work
- cursor pagination by post ID; no OFFSET

## Fresh schema foundation

`src/backend/v2/internal/schema/migrations/001_core.sql` defines fresh v2 tables for:

- `users`
- `user_roles`
- `user_credentials`
- `user_identities`
- `invitations`
- `posts`
- `media`
- `tags`
- `post_tags`
- nested `comments`
- `post_votes`
- `comment_votes`
- durable `media_jobs`

Important modeling decisions:

- immutable numeric bigint IDs are relational identity
- username is never a foreign key
- credentials and external identities are separate from users
- invitation policy is separate from credential/identity design
- post content filter and release state are explicit
- media processing state is explicit
- tag removal preserves moderator/user audit metadata
- media jobs are durable PostgreSQL rows with availability/attempt/lease fields

Indexes are intentionally limited to the initial real query shapes. Do not add speculative indexes before the server plans are captured.

## Core API/query slice

Implemented endpoints:

- `GET /healthz`
- `GET /api/v2/feed?before=<post-id>&limit=<n>&q=<search>`
- `GET /api/v2/posts/<id>/around?radius=<n>&q=<search>`

The feed path:

- orders by post ID descending
- uses `p.id < cursor`
- requests `limit + 1` to derive the next cursor
- returns only explicitly released/non-deleted posts with ready media
- defaults current unauthenticated skeleton visibility to `sfw`
- has a hard page cap of 120

Around-post reconstruction uses two bounded ID scans rather than OFFSET: newer IDs ascending up to the radius, older/current IDs descending up to radius + 1, then combines them in descending board order.

All SQL values are parameterized. Search score operator text is selected only from a closed typed enum.

## Search parser

A real lexer/parser/AST is implemented; query strings are not split ad hoc.

Current grammar supports:

- included tags: `cat landscape`
- excluded tags: `-anime`
- quoted tag names
- one score predicate such as `score:>=100` or `score:<-10`

It normalizes/deduplicates tags, limits query complexity, and rejects malformed predicates. SQL compilation remains separate from parsing.

## Tests/checks completed in the Astra environment

The available execution environment has Go 1.23.2 and no PostgreSQL server, while this v2 module intentionally requires Go 1.25 because of the chosen current pgx release.

Completed checks:

- pure package tests pass for `search`, `feed`, `httpapi`, `querysql`, and `schema` using a temporary local-only Go directive downgrade with the workspace disabled;
- tests cover parser behavior/errors, feed cursor/page contract, filter normalization, around-post selected-post semantics, HTTP error/deadline behavior, SQL parameterization/no-OFFSET shape, around-query bounds, and embedded migration presence;
- complete module source including `cmd/api` and the PostgreSQL store typechecked/tested against a temporary local-only pgx API stub;
- no stub and no temporary Go-version change is committed;
- source was gofmt'd before commit.

Not validated here and therefore not claimed:

- real pgx compilation against Go 1.25+
- actual PostgreSQL migration execution
- SQL planner behavior
- PostgreSQL scan/runtime behavior
- target-server API latency/concurrency

## Server benchmark fixtures

`src/backend/v2/bench/seed.sql` creates a disposable benchmark set of:

- 1,000 users
- 100,000 released posts
- 100,000 media rows
- 100 tags
- roughly three active tags per post

`src/backend/v2/bench/explain.sql` captures `EXPLAIN (ANALYZE, BUFFERS)` for:

- first feed page
- old-cursor feed page
- required tag + score
- required + excluded tag
- around-post reconstruction

Detailed procedure: `docs/v2/M2_RUN.md`.

## Performance risks to answer with evidence

1. Does `posts_feed_released_idx` give a bounded backward scan for normal and old-cursor pages?
2. Does required-tag search scan too much of the feed because of the initial correlated `EXISTS` shape?
3. Does excluded-tag filtering create meaningful extra work?
4. Does the two-sided around-post query remain bounded around old IDs?
5. Is an 8-connection pool sufficient on the shared i7-7700 host, or does measured contention justify another value?
6. Is PostgreSQL/query work dominant, or is API encoding/Go overhead material?

If tag search is weak, improve relational/query shape before adding cache complexity. Do not introduce Redis merely to hide an unmeasured SQL plan.

## M2 gate outstanding

On the target server, in an isolated M2 worktree and disposable database:

- use real Go 1.25+
- run `go mod tidy` and commit the resulting `go.sum` only after successful dependency-backed validation
- run `go test ./...`
- apply `001_core.sql`
- run the 100k-post seed
- capture all `EXPLAIN (ANALYZE, BUFFERS)` plans
- smoke test feed/search/around endpoints
- benchmark representative HTTP shapes at concurrency 1 / 4 / 8 and enough additional load to expose pool saturation if present
- record p50/p95/p99, requests/sec, errors, CPU, and PostgreSQL connection behavior
- make query/index changes only from those measurements, with before/after evidence

Do not merge M2 into `v2` until this gate is reviewed.

## Single best next task

Run `docs/v2/M2_RUN.md` on the remote target server against `astra/m2-core-schema-api`: validate with real Go/pgx, apply and seed the disposable PostgreSQL schema, collect the five query plans and API concurrency measurements, then use those results to tune only demonstrated query/index bottlenecks before deciding whether this first M2 slice can integrate into `v2`.