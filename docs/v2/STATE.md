# Ginbar v2 State / Handoff

Last updated: 2026-10-01
Phase: M2 core schema/API implemented; Go/pgx validation and Docker builds pass; PostgreSQL benchmark gate still outstanding after two harness-only failures
Integration branch: `v2`
Active implementation branch: `astra/m2-core-schema-api`
Completed M1 branch: `astra/m1-scroll-anchor`
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the resume point. Read it before `PLAN.md`. Do not rely on chat history as project memory.

## Branch status

- `master` remains untouched by rewrite work.
- M1 is complete and integrated into `v2` at `32e154c4541fcdd2c24c1c05ed278e2dbddef960`.
- `astra/m2-core-schema-api` is a clean descendant of that `v2` head; do not merge until the M2 server gate passes.
- The M2 branch contains the clean service/search/API slice, fresh PostgreSQL schema/store/query compiler, deterministic benchmark fixtures/load generator, committed real Go module graph, and execution-only target-server gate.

## M1 decisions retained

- SolidJS + TypeScript + Vite accepted.
- Keep incremental retention + CSS containment; no virtualization without evidence.
- Browser/frontend measurements belong to the machine running the browser.
- Remote target-server measurements are authoritative for backend/server-side work.

## M2 architecture

The clean v2 backend lives at `src/backend/v2`. Legacy `src/backend` / Wallium code is reference-only.

Current choices:

- Go 1.25;
- direct `pgx/v5`, pgx 5.11.0;
- standard `net/http`, no web framework;
- PostgreSQL authoritative for application state and durable media jobs;
- no Redis dependency until a measured ephemeral use exists;
- initial PostgreSQL pool cap 8;
- 3-second current feed DB/request deadline;
- post-ID cursor pagination only, no OFFSET.

Fresh schema covers users, roles, credentials, external identities, invitations, posts, media, tags/tag audit, nested comments, post/comment votes, and durable media jobs.

Identity uses immutable numeric IDs; usernames are never foreign keys. Invitation policy is separate from credentials/identity. Processing/release/filter/job state is explicit.

## Core API/query slice

Endpoints:

- `GET /healthz`
- `GET /api/v2/feed?before=<id>&limit=<n>&q=<search>`
- `GET /api/v2/posts/<id>/around?radius=<n>&q=<search>`

Feed queries order by ID descending, use `p.id < cursor`, fetch `limit + 1`, return released/non-deleted posts with ready media, default the current skeleton to SFW visibility, and cap pages at 120.

Around-post reconstruction uses bounded scans on both sides of the selected ID, not OFFSET.

Search uses a real lexer/parser/AST supporting included tags, excluded tags, quoted tags, and one score predicate such as `score:>=100`. SQL values are parameterized; score operator text comes only from a closed typed enum.

## Real target-server validation established

The execution-only agent has now run the M2 branch twice on the target i7-7700 server using Docker Go 1.25.14 and a dedicated PostgreSQL 17.11 container isolated from Wallium.

Confirmed across the runs:

- `go mod tidy` succeeds;
- real `go test ./...` with pgx succeeds for every package;
- committed `go.mod` and `go.sum` are byte-identical to the Go 1.25.14 tidy output;
- generated `go.sum` is 26 lines, SHA-256 `a5e7a8db07eba28dbde48bf9a54c4e9084a5a95ded581c0ff44c08b74e3dfe69`;
- API Docker build succeeds; measured binary size on the second run: 14,955,020 bytes;
- stdlib HTTP benchmark Docker build succeeds; measured binary size: 8,576,020 bytes;
- benchmark worktrees remained clean;
- deployed Wallium/master was untouched; Wallium remained running;
- disposable PostgreSQL containers/volumes were removed after each failed run.

## Harness failures and fixes

### Attempt 1

Tested `0b1727ffa1638d43b8800170d005b119f591cc67`.

Failure: Docker `go build -o <host-temp-path>` wrote binaries only inside the ephemeral Go container, so host `stat` failed before schema work.

Fix: build through a bind-mounted `/out` directory and verify both executables before database preparation.

### Attempt 2

Tested `7cec3af902de08ef6ea5ad224b4e5a5e09695a8c`.

The build-output fix worked: both binaries were present and measured. The run then stopped before SQL execution with:

`psql: error: /tmp/ginbar-m2-work.../src/backend/v2/internal/schema/migrations/001_core.sql: No such file or directory`

The migration was present on the host temporary archive; Go tests embedding the migrations passed. Root cause: when host `psql` is absent, `psql_exec` launches a disposable PostgreSQL client container. Passing `-f <host-path>` gave that container a host-only path that was not mounted.

Fix now committed:

- Docker `psql` runs with stdin attached (`docker run -i`);
- schema, seed, and EXPLAIN files are streamed through stdin instead of passed with host `-f` paths;
- runner explicitly verifies all three SQL files exist after the Git archive step;
- runner performs a `select 1` stdin probe through the selected psql mode before Go builds;
- benchmark case TSV is generated with `printf` to guarantee real tab separators;
- `M2_RUN.md` now waits for a successful query against `ginbar_m2_bench`, not merely `pg_isready`, avoiding PostgreSQL init false positives.

Both failed runs are harness-only evidence; no PostgreSQL query/runtime or API performance conclusion can be drawn yet.

Preserved target-server result directories reported by the agent:

- `/tmp/ginbar-m2-gate-20261001T201343Z-shared`
- `/tmp/ginbar-m2-gate-20261001T210956Z-shared`

## Prepared server gate

`docs/v2/M2_RUN.md` is authoritative.

The gate uses a dedicated disposable PostgreSQL 17 container on loopback, never Wallium data. It seeds 1,000 users, 100,000 posts, 100,000 media rows, 100 tags, and about three tags/post.

It captures `EXPLAIN (ANALYZE, BUFFERS)` for:

1. first feed page;
2. old-cursor feed page;
3. required tag + score;
4. required + excluded tag;
5. around-post reconstruction.

HTTP cases run at concurrency 1 / 4 / 8 / 16 / 32 and collect p50/p95/p99/max, requests/sec, errors, API CPU/RSS, system load, PostgreSQL connection counts, relation/index sizes, and DB counters.

A quiet-host comparison may stop only Wallium backend/worker when clearly safe and must restore them. Pool 16 is measured only if pool-8 evidence shows saturation with CPU headroom.

## Performance questions still unanswered

1. Does the released-post index provide bounded cursor scans?
2. Do correlated tag `EXISTS` clauses scan excessive feed rows?
3. What is excluded-tag overhead?
4. Is around-post reconstruction bounded around old IDs?
5. Does pool 8 queue before CPU becomes limiting?
6. Is PostgreSQL or Go/JSON dominant?
7. What is Wallium shared-host interference?

Do not add Redis or speculative indexes before these measurements.

## M2 gate status

M2 is not ready to merge into `v2`.

Real Go/pgx compilation, tests, dependency reproducibility, and Docker builds are validated. PostgreSQL migration/seed execution, query plans, endpoint latency/concurrency, and server resource behavior remain outstanding.

## Single best next task

Rerun `docs/v2/M2_RUN.md` from the current `origin/astra/m2-core-schema-api` head using the execution-only local agent. Return information only: clean module drift check, schema/seed result, all five query plans, complete HTTP matrix, resource/connection observations, and optional safe Wallium quiet-host comparison. Use that evidence here to make any query/index decision before M2 integration.
