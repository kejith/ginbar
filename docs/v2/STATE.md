# Ginbar v2 State / Handoff

Last updated: 2026-10-01
Phase: M2 core schema/API implemented; media and tag query plans validated; search smoke parser bug fixed; complete HTTP gate outstanding
Integration branch: `v2`
Active implementation branch: `astra/m2-core-schema-api`
Completed M1 branch: `astra/m1-scroll-anchor`
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the resume point. Read it before `PLAN.md`. Do not rely on chat history as project memory.

## Branch status

- `master` remains untouched by rewrite work.
- M1 is complete and integrated into `v2` at `32e154c4541fcdd2c24c1c05ed278e2dbddef960`.
- `astra/m2-core-schema-api` remains a clean descendant of that `v2` head; do not merge until the M2 HTTP/resource gate passes.
- M2 contains the clean Go API/domain/search slice, fresh PostgreSQL schema/store/query compiler, real Go 1.25 dependency graph, deterministic load generator, and execution-only target-server benchmark harness.

## M1 decisions retained

- SolidJS + TypeScript + Vite accepted.
- Keep incremental retention + CSS containment; no virtualization without evidence.
- Browser/frontend timings belong to the client machine.
- Target-server measurements are authoritative for backend/server-side work.

## M2 architecture

Backend: `src/backend/v2`. Legacy Wallium backend is reference-only.

Current choices:

- Go 1.25, direct pgx/v5 5.11.0, standard `net/http`;
- PostgreSQL authoritative for application and durable job state;
- no Redis dependency until a measured ephemeral use exists;
- initial pool cap 8, current request/DB deadline 3 seconds;
- post-ID cursor pagination only, never OFFSET;
- real lexer/parser/AST for included/excluded tags and score predicates;
- immutable numeric relational IDs; usernames never foreign keys.

Endpoints:

- `GET /healthz`
- `GET /api/v2/feed?before=<id>&limit=<n>&q=<search>`
- `GET /api/v2/posts/<id>/around?radius=<n>&q=<search>`

## Repeated target-server validation

Target: Ubuntu 24.04 / i7-7700 4C/8T / 62 GiB RAM. Runs use Docker Go 1.25.14 and isolated PostgreSQL 17.11 on loopback, never Wallium data.

Repeatedly confirmed:

- `go mod tidy` passes;
- real `go test ./...` with pgx passes;
- committed `go.mod` / `go.sum` match tidy output byte-for-byte;
- API and stdlib HTTP benchmark binaries build successfully;
- schema and deterministic 100k-post seed execute successfully;
- benchmark worktrees stay clean;
- Wallium remains running and production checkouts are untouched;
- disposable benchmark DB resources are removed after runs.

## Harness history

Benchmark-only failures already fixed:

1. Docker Go output now persists through bind-mounted `/out`.
2. Dockerized `psql` receives schema/seed/EXPLAIN SQL through stdin rather than host-only file paths.
3. Benchmark API auto-selects a free loopback port, rejects an occupied explicit port, and verifies its own PID before accepting health.
4. `GINBAR_BENCH_PG_CONTAINER` enables low-overhead connection sampling through `docker exec` in the existing disposable database container.

## Validated query decisions

### Bounded media lookup — accepted

Production feed/around queries use a bounded lateral ready-media lookup by `media.post_id`.

Run 4 same-database evidence:

- old cursor baseline: 18.447 ms / 1,246 hits / about 50,077 media rows scanned;
- bounded lookup: 0.357 ms / 186 hits / 61 `media_pkey` lookups;
- about 51.7x faster in that pair;
- first-feed difference was only 0.035 ms and both forms were sub-millisecond.

Run 5 retained the result: old cursor EXPLAIN was 0.316 ms with 61 bounded media lookups.

Decision: keep the bounded media lookup.

### Around-post correctness — accepted

The reserved derived-table alias `window` was replaced with `combined_posts`. Run 5 around-post reconstruction completed in 0.413 ms for 61 rows with bounded scans on both sides.

### Resolved tag-ID include filter — accepted

The first tag rewrite did not change PostgreSQL's plan. The current shape first resolves `tags.normalized_name` to immutable `tags.id`, then filters `post_tags.tag_id` with `removed_at IS NULL`.

Run 5 tested `a3ea24b28591f14219ecbc0aa812e468b831dba3` and proved the existing partial index is sufficient; no new index is currently justified.

Current plans:

- required tag + score: 4.390 ms, 1,825 shared hits;
- required + excluded tag + score: 2.905 ms, 2,557 shared hits;
- both inclusion paths use `post_tags_tag_post_active_idx`;
- included assignment scan is 1,587 rows instead of about 158,680;
- excluded-tag work remains bounded per candidate post.

Same-run baseline -> tuned:

- required tag + score: 71.240 -> 1.563 ms, about 45.6x faster;
- required + excluded tag + score: 70.068 -> 1.899 ms, about 36.9x faster;
- old cursor: 9.568 -> 0.156 ms, about 61.3x faster;
- first feed: 0.174 -> 0.148 ms.

Decision: keep the resolved tag-ID shape and existing indexes. Do not add another search index without new evidence.

## Run 5 HTTP smoke failure and fix

Run 5 successfully avoided occupied port 18080 and selected 18081. `pg_sample_mode=container` was correctly recorded.

Health and unfiltered feed smoke requests passed. The exact search smoke:

`q=tag-42 score:>=100`

returned HTTP 400 before the load matrix.

Root cause is in the search lexer, not PostgreSQL: unquoted word scanning treated every internal `-` as the start of exclusion syntax, so `tag-42` was tokenized incorrectly. The lexer now treats `-` as structural when `next()` begins on it (`-tag` exclusion or negative score), while hyphens encountered inside an unquoted word remain part of that tag.

Added regressions:

- parser accepts `tag-42 -other-tag score:>=100`;
- existing `-anime` exclusion remains valid;
- existing `score:<-10` remains valid;
- HTTP handler test covers the exact failed benchmark URL and verifies parsed tag `tag-42` + score 100.

Local isolated checks after the fix:

- search parser tests pass under local Go 1.23.2;
- isolated HTTP handler test for the exact smoke URL returns 200 and parses the expected search AST.

Real Go 1.25 + PostgreSQL rerun of the current head is still required before the M2 gate can close.

## M2 gate status

M2 is not ready to merge into `v2` yet.

Query-plan work is now satisfactory on the 100k deterministic dataset. The remaining gate is API/load behavior on the target server:

- real Go 1.25 tests/build on the current search-fix head;
- successful health/feed/search/around smoke checks;
- complete 5x5 HTTP matrix at concurrency 1 / 4 / 8 / 16 / 32;
- API CPU/RSS and server load;
- PostgreSQL total/active connection behavior;
- pool-size decision only if measured saturation justifies it.

Pool 16 must not be tested unless p95 degrades at higher concurrency, active connections repeatedly reach 8, and CPU still has headroom.

Quiet-host Wallium comparison remains optional and should be skipped unless stopping/restoring only backend/worker is clearly safe.

## Single best next task

Use the execution-only local agent to rerun `docs/v2/M2_RUN.md` from the current exact `origin/astra/m2-core-schema-api` head. Pass `GINBAR_BENCH_PG_CONTAINER`. Return successful smoke results, all five plans, complete 5x5 HTTP matrix, resource/connection samples, and cleanup state. Do not modify code or indexes on the server. If the full gate passes, use the measurements here to make the pool decision and determine whether M2 can integrate into `v2`.
