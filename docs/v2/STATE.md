# Ginbar v2 State / Handoff

Last updated: 2026-10-01
Phase: M2 core schema/API first slice implemented; execution-only target-server PostgreSQL benchmark gate outstanding
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
- The M2 branch is a clean descendant of `v2`; do not merge it until the real Go/PostgreSQL/server gate below passes.
- M2 implementation/documentation commits include:
  - `7c3b6b2ac1599b5cb1b04e9a73722197011c9805` — search/feed/domain/HTTP service contracts
  - `caee8b57455be9ea460594896d6e828fa3b1d2db` — PostgreSQL schema, query compiler/store, API executable, benchmark fixtures
  - `b5f69dee11048d9ff7a3f9bde15926f125200b9c` — M2 architecture docs and workspace switch
  - `16d4f89df418233ae2b00f5dc60220ff3c904b0d` — first server-benchmark handoff
  - `d651edf598da15277fa4697064c7f6819b0f1cc9` — standard-library deterministic HTTP load generator
  - `39af2e303ad08077cd85db15e74cf9c3077b50d7` — isolated target-server gate runner
  - `1f1d721e884335b99aa6ac12eac1963307ebf9ff` — execution-only M2 runbook

## M1 decision retained

- SolidJS is accepted for the v2 frontend.
- Keep incremental retention + CSS containment; do not add virtualization without later evidence.
- Frontend/browser timings belong to the client machine running the browser.
- Target-server benchmarking is authoritative only for backend/server-side implementation.

## M2 architecture in this slice

M2 is a clean v2 backend module at `src/backend/v2`. The legacy `src/backend` / `wallium` implementation is reference-only and is not extended.

The root Go workspace points only at `src/backend/v2`; the stale legacy workspace sum was removed.

Current backend choices:

- Go 1.25 minimum
- direct `pgx/v5` PostgreSQL interface, pinned to 5.11.0
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

## Checks completed before target-server execution

The Astra execution environment has Go 1.23.2 and no PostgreSQL server, while the v2 module requires Go 1.25.

Completed checks:

- pure package tests pass for `search`, `feed`, `httpapi`, `querysql`, and `schema` using a temporary local-only Go directive downgrade with the workspace disabled;
- tests cover parser behavior/errors, feed cursor/page contract, filter normalization, around-post selected-post semantics, HTTP error/deadline behavior, SQL parameterization/no-OFFSET shape, around-query bounds, and embedded migration presence;
- complete module source including `cmd/api` and the PostgreSQL store typechecked/tested against a temporary local-only pgx API stub;
- no stub and no temporary Go-version change is committed;
- source was gofmt'd before commit;
- `bench/httpbench.go` was compiled/exercised with the available Go toolchain as a standalone stdlib program;
- `bench/run_gate.sh` passes shell syntax checking.

Not validated here and therefore not claimed:

- real pgx compilation against Go 1.25+
- actual PostgreSQL migration execution
- SQL planner behavior
- PostgreSQL scan/runtime behavior
- target-server API latency/concurrency

## Execution-only target-server harness

`src/backend/v2/bench/run_gate.sh` is prepared so the external/local agent does not need to write or modify code.

Properties:

- it archives the exact checked-out Git HEAD into `/tmp` before Go commands, so `go mod tidy`, generated `go.sum`, tests, and builds do not modify the worktree;
- it refuses any PostgreSQL database not named `ginbar_m2_bench` or `ginbar_m2_bench_*`;
- it uses host Go 1.25+ when available, otherwise an existing Docker installation with `golang:1.25`;
- it applies/loads the prepared schema and 100k-post seed only on the disposable database;
- it captures the five prepared `EXPLAIN (ANALYZE, BUFFERS)` plans;
- it builds and runs the API on loopback with an 8-connection pool;
- it runs the prepared stdlib HTTP load generator at concurrency 1 / 4 / 8 / 16 / 32;
- it records p50/p95/p99/max, requests/sec, status/errors, API CPU/RSS, system load, PostgreSQL connection counts, relation/index sizes, and database statistics;
- all results are written outside the repository under `/tmp/ginbar-m2-gate-*`.

`docs/v2/M2_RUN.md` is now the exact execution procedure. The agent performing this gate must not edit, tune, commit, merge, push, or update project docs; it should return measurements/information only.

A normal shared-host run should be collected first. If Wallium can be safely stopped without persistent configuration changes, a second HTTP-only run may be collected with Wallium temporarily stopped and then restored. A 16-connection pool probe is allowed only when the 8-connection data shows a clear high-concurrency latency cliff while CPU still has substantial headroom.

## Performance questions to answer with evidence

1. Does `posts_feed_released_idx` give a bounded backward scan for normal and old-cursor pages?
2. Does required-tag search scan too much of the feed because of the initial correlated `EXISTS` shape?
3. Does excluded-tag filtering create meaningful extra work?
4. Does the two-sided around-post query remain bounded around old IDs?
5. Does the 8-connection pool create measurable queueing before server/PostgreSQL CPU becomes limiting?
6. Is PostgreSQL/query work dominant, or is Go/JSON/API overhead material?
7. How much does the normal shared Wallium workload change latency versus a controlled quiet-host run?

If tag search is weak, improve relational/query shape before adding cache complexity. Do not introduce Redis merely to hide an unmeasured SQL plan.

## M2 gate outstanding

On the target server, from an isolated worktree and disposable database:

- run the prepared execution-only gate exactly as documented in `docs/v2/M2_RUN.md`;
- return the real dependency/test result and generated dependency information;
- return all five query-plan headline findings;
- return the HTTP latency/throughput matrix and resource/connection observations;
- return shared-host versus quiet-host data if Wallium can be safely paused;
- do not modify source based on the findings.

Do not merge M2 into `v2` until the measurements are reviewed here.

## Single best next task

Have the local/remote-capable agent execute `docs/v2/M2_RUN.md` against the current `astra/m2-core-schema-api` HEAD and return information only: real Go/pgx validation, disposable PostgreSQL migration/seed result, all five query plans, the fixed HTTP concurrency matrix, server/resource observations, and the optional Wallium quiet-host comparison. Use that evidence in the primary engineering session to decide the first M2 query/index changes.