# Ginbar v2 State / Handoff

Last updated: 2026-10-02
Phase: M2 core schema/API implemented; feed/search plans validated; direct-link around reconstruction fixed; complete HTTP/resource gate outstanding
Integration branch: `v2`
Active implementation branch: `astra/m2-core-schema-api`
Completed M1 branch: `astra/m1-scroll-anchor`
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the resume point. Read it before `PLAN.md`. Do not rely on chat history as project memory.

## Branch status

- `master` remains untouched by rewrite work.
- M1 is complete and integrated into `v2` at `32e154c4541fcdd2c24c1c05ed278e2dbddef960`.
- `astra/m2-core-schema-api` is a clean descendant of that `v2` head; do not merge until the M2 HTTP/resource gate passes.
- M2 contains the clean Go API/domain/search slice, fresh PostgreSQL schema/store/query compiler, real Go 1.25 dependency graph, deterministic load generator, and execution-only target-server benchmark harness.

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

Harness now also:

- persists Docker Go output through bind-mounted `/out`;
- streams SQL into Dockerized `psql` over stdin;
- auto-selects a free loopback API port and verifies its own API PID;
- uses `GINBAR_BENCH_PG_CONTAINER` for low-overhead connection sampling from the existing disposable DB container.

## Validated query decisions

### Bounded media lookup — accepted

Feed/around use bounded lateral ready-media lookup by `media.post_id`.

Same-database evidence:

- old cursor baseline scanned about 50,077 media rows;
- tuned shape performs 61 `media_pkey` lookups;
- Run 5 comparison: 9.568 -> 0.156 ms, about 61x faster;
- Run 6 old-cursor plan: 0.200 ms / 189 hits.

Keep this shape.

### Resolved tag-ID include filter — accepted

Included tag names resolve to immutable `tags.id`, then constrain `post_tags.tag_id` with `removed_at IS NULL`.

Existing partial index is sufficient:

`post_tags_tag_post_active_idx (tag_id, post_id DESC) WHERE removed_at IS NULL`

Run 5 same-database baseline -> tuned:

- required tag + score: 71.240 -> 1.563 ms;
- required + excluded + score: 70.068 -> 1.899 ms;
- included assignment scan: about 158,680 -> 1,587 rows.

Run 6 retained the plan:

- required tag + score: 2.916 ms / 1,825 hits;
- required + excluded + score: 1.879 ms / 2,557 hits;
- both use `post_tags_tag_post_active_idx` and scan 1,587 included assignments.

Keep this shape. Do not add another search index without new evidence.

### Hyphenated search tags — fixed and validated

Run 5 exposed HTTP 400 for `q=tag-42 score:>=100` because the lexer treated internal `-` as exclusion syntax.

The lexer now keeps internal hyphens inside unquoted tag words while preserving `-tag` exclusion and negative scores.

Run 6 real Go 1.25 tests passed and the exact search smoke returned HTTP success with valid feed JSON.

## Run 6 direct-link failure

Run 6 tested `50ec3a7ccb8fee6e81819299f5332fa4e0eca3ac`.

Validation:

- Go 1.25.14 tests passed;
- schema: 355 ms;
- seed: 33,611 ms;
- free API port selection worked (`18081`);
- `pg_sample_mode=container` recorded;
- health, unfiltered feed, and hyphenated-tag search smoke checks passed.

The fourth smoke request:

`GET /api/v2/posts/50000/around?radius=30`

returned 404 before the HTTP matrix.

Root cause:

- deterministic seed post 50000 has `content_filter=2` (NSFW);
- the around endpoint defaults surrounding context to SFW;
- old SQL put the selected post inside the filtered `older` branch (`p.id <= $1 AND content_filter=0`);
- SQL returned 61 surrounding SFW rows but omitted post 50000;
- feed service requires the selected ID to be present and therefore correctly returned `ErrPostNotFound` / HTTP 404.

This is a real canonical direct-link reconstruction bug, not a benchmark issue.

## Current around-post fix

`BuildAround` now has three bounded branches:

1. `newer`: IDs above selected, context filters/search applied, limit radius;
2. `selected`: exact selected ID, release/deletion/media-readiness constraints only;
3. `older`: IDs below selected, context filters/search applied, limit radius.

The selected post is therefore not hidden by feed/search context, while genuinely unreleased/deleted/no-ready-media posts still remain unavailable and result in 404.

For radius 30 the response remains bounded to at most 61 posts: 30 newer + selected + 30 older.

The benchmark around EXPLAIN fixture mirrors this exact shape and intentionally continues using post 50000, so the smoke test exercises an NSFW canonical selected post surrounded by default-SFW context.

Regression tests assert:

- strict `>` / `<` side bounds;
- only the two context branches receive content/search predicates;
- the selected branch targets `$1` without context filter/search clauses;
- all three branches use bounded media lookup;
- no OFFSET is introduced.

An isolated local Go 1.23.2 query-builder test passed after this change. Real Go 1.25 + PostgreSQL validation of the current head remains required.

## M2 gate status

M2 is not ready to merge into `v2` yet.

Query-plan work is satisfactory on the deterministic 100k dataset. Remaining gate:

- real Go 1.25 tests/build on the current around-fix head;
- successful health/feed/search/around smoke checks;
- around EXPLAIN confirms the three-branch bounded shape;
- complete 5x5 HTTP matrix at concurrency 1 / 4 / 8 / 16 / 32;
- API CPU/RSS and server load;
- PostgreSQL total/active connection behavior;
- pool-size decision only if measured saturation justifies it.

Pool 16 must not be tested unless p95 degrades at higher concurrency, active connections repeatedly reach 8, and CPU still has headroom.

Quiet-host Wallium comparison is optional and should be skipped unless stopping/restoring only backend/worker is clearly safe.

## Single best next task

Use the execution-only local agent to rerun `docs/v2/M2_RUN.md` from the current exact `origin/astra/m2-core-schema-api` head with `GINBAR_BENCH_PG_CONTAINER`. Confirm all four smoke checks, return all five plans, complete the 5x5 HTTP matrix, and return CPU/RSS/load/PostgreSQL connection samples. Do not modify code or indexes on the server. If the full gate passes, decide here whether pool 8 is sufficient and whether M2 can integrate into `v2`.
