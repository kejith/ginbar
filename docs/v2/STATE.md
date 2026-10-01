# Ginbar v2 State / Handoff

Last updated: 2026-10-01
Phase: M2 core schema/API implemented; first server validation passed Go/pgx tests but benchmark harness failed before PostgreSQL execution; fixed rerun outstanding
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
- M2 implementation commits include the clean service/search/API slice, fresh PostgreSQL schema/store/query compiler, benchmark fixtures, deterministic HTTP load generator, execution-only gate runner, and runbook.
- First benchmark target was `0b1727ffa1638d43b8800170d005b119f591cc67`.
- After that run, the validated Go module graph was committed and the container-build output defect in `bench/run_gate.sh` was fixed.

## M1 decisions retained

- SolidJS + TypeScript + Vite accepted.
- Keep incremental retention + CSS containment; no virtualization without evidence.
- Browser/frontend measurements belong to the machine running the browser.
- Remote target-server measurements are authoritative for backend/server-side work.

## M2 architecture

The clean v2 backend lives at `src/backend/v2`. Legacy `src/backend` / Wallium code is reference-only.

Current choices:

- Go 1.25;
- direct `pgx/v5` PostgreSQL interface, pgx 5.11.0;
- standard `net/http`, no web framework;
- PostgreSQL authoritative for application state and durable media jobs;
- no Redis dependency until a measured ephemeral use exists;
- initial PostgreSQL pool cap 8;
- 3-second current feed DB/request deadline;
- post-ID cursor pagination only, no OFFSET.

Fresh schema covers users, roles, credentials, external identities, invitations, posts, media, tags, tag assignments/removal audit, nested comments, post/comment votes, and durable media jobs.

Identity uses immutable numeric IDs; usernames are never foreign keys. Invitation policy is separate from credentials/identity. Processing/release/filter/job state is explicit.

## Core API/query slice

Endpoints:

- `GET /healthz`
- `GET /api/v2/feed?before=<id>&limit=<n>&q=<search>`
- `GET /api/v2/posts/<id>/around?radius=<n>&q=<search>`

Feed queries:

- order by post ID descending;
- use `p.id < cursor`;
- fetch `limit + 1` for cursor derivation;
- return released/non-deleted posts with ready media;
- current skeleton defaults to SFW visibility;
- hard page cap 120.

Around-post reconstruction uses bounded scans on both sides of the selected ID, not OFFSET.

Search uses a real lexer/parser/AST supporting included tags, excluded tags, quoted tags, and one score predicate such as `score:>=100`. SQL values are parameterized; score operator text comes only from a closed typed enum.

## First real server validation

Execution-only agent tested exact commit `0b1727ffa1638d43b8800170d005b119f591cc67` on the target i7-7700 server using Docker Go 1.25.14 and a dedicated PostgreSQL 17.11 container isolated from Wallium.

Confirmed:

- `go mod tidy` succeeded;
- real `go test ./...` with pgx succeeded for every package;
- generated `go.sum` contained 26 lines, SHA-256 `a5e7a8db07eba28dbde48bf9a54c4e9084a5a95ded581c0ff44c08b74e3dfe69`;
- tidy added indirect requirements for pgpassfile, pgservicefile, puddle/v2, `x/sync`, and `x/text`;
- the benchmark checkout stayed clean;
- production Wallium/master was untouched and Wallium stayed running.

The exact tidied `go.mod` and server-generated `go.sum` are now committed. The committed `go.sum` Git blob is `3f716dd12523b6194234f0bb27a6cf980de0846a`, matching the uploaded server artifact byte-for-byte.

## Harness failure and fix

The first run produced no PostgreSQL plans or HTTP measurements because the gate stopped immediately after successful builds.

Cause: in Docker Go mode, `go build -o "$work_dir/..."` referenced a host path that was not bind-mounted into the ephemeral Go container. Builds returned success but the binaries disappeared with the container; the subsequent host `stat` failed.

This was a benchmark-harness defect, not backend performance/correctness evidence.

`bench/run_gate.sh` now:

- creates a dedicated host `bin` directory under its temporary work area;
- bind-mounts that directory as `/out` for Docker builds;
- writes API/httpbench binaries through `/out`;
- explicitly verifies both executables exist before schema/seed work;
- records both binary sizes.

The original failed result bundle is preserved externally as `/tmp/ginbar-m2-gate-20261001T201343Z-shared` on the target server and was also returned to the primary session.

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

A quiet-host comparison may stop only Wallium backend/worker when safe and must restore them. Pool 16 is measured only if the pool-8 evidence shows saturation with CPU headroom.

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

Real Go/pgx compilation and tests are now validated. PostgreSQL migration execution, query plans, endpoint latency/concurrency, and server resource behavior remain outstanding because the first run stopped at the repaired harness boundary.

## Single best next task

Rerun `docs/v2/M2_RUN.md` on the target server from the current `origin/astra/m2-core-schema-api` head using the execution-only local agent. Return information only: module-file drift check, schema/seed result, all five query plans, complete HTTP matrix, resource/connection observations, and optional Wallium quiet-host comparison. Use that evidence here to decide any query/index changes before M2 integration.
