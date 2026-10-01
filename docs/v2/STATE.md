# Ginbar v2 State / Handoff

Last updated: 2026-10-01
Phase: M2 core schema/API implemented; target-server PostgreSQL evidence now partially captured; measured query-shape fixes implemented; before/after rerun outstanding
Integration branch: `v2`
Active implementation branch: `astra/m2-core-schema-api`
Completed M1 branch: `astra/m1-scroll-anchor`
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the resume point. Read it before `PLAN.md`. Do not rely on chat history as project memory.

## Branch status

- `master` remains untouched by rewrite work.
- M1 is complete and integrated into `v2` at `32e154c4541fcdd2c24c1c05ed278e2dbddef960`.
- `astra/m2-core-schema-api` is a clean descendant of that `v2` head; do not merge until the M2 gate passes.
- The M2 branch contains the clean service/search/API slice, fresh PostgreSQL schema/store/query compiler, real Go 1.25 dependency graph, deterministic HTTP load generator, and execution-only server benchmark harness.

## M1 decisions retained

- SolidJS + TypeScript + Vite accepted.
- Keep incremental retention + CSS containment; no virtualization without evidence.
- Browser/frontend measurements belong to the client running the browser.
- Remote target-server measurements are authoritative for backend/server-side work.

## M2 architecture

The clean v2 backend lives at `src/backend/v2`. Legacy `src/backend` / Wallium code is reference-only.

Current choices:

- Go 1.25;
- direct `pgx/v5` 5.11.0;
- standard `net/http`;
- PostgreSQL authoritative for application state and durable media jobs;
- no Redis dependency without a measured ephemeral use;
- initial PostgreSQL pool cap 8;
- current request/DB deadline 3 seconds;
- post-ID cursor pagination only, no OFFSET.

Fresh schema covers users, roles, credentials, external identities, invitations, posts, media, tags/tag audit, nested comments, post/comment votes, and durable media jobs. Relational identity uses immutable numeric IDs; usernames are never foreign keys.

## Core API/query slice

Endpoints:

- `GET /healthz`
- `GET /api/v2/feed?before=<id>&limit=<n>&q=<search>`
- `GET /api/v2/posts/<id>/around?radius=<n>&q=<search>`

Search uses a real lexer/parser/AST with included/excluded tags, quoted tags, and score predicates. SQL values are parameterized; score operators come from a closed typed enum.

## Real target-server validation

Target server: Ubuntu 24.04 / i7-7700 4C/8T / 62 GiB RAM. Server runs used Docker Go 1.25.14 and isolated PostgreSQL 17.11 on loopback, separate from Wallium.

Repeatedly validated:

- `go mod tidy` succeeds;
- `go test ./...` succeeds with real pgx;
- committed `go.mod` and `go.sum` are byte-identical to Go 1.25.14 tidy output;
- API Docker build succeeds: 14,955,020 bytes in the latest measured run;
- HTTP benchmark build succeeds: 8,576,020 bytes;
- benchmark worktrees remain clean;
- Wallium/master is untouched and Wallium remains running;
- disposable benchmark database containers/volumes are cleaned up.

## Benchmark harness history

Attempt 1 fixed Docker Go binary output persistence by building through a bind-mounted `/out` directory.

Attempt 2 fixed Dockerized `psql` host-path handling by streaming SQL through stdin; also added required-file checks, an stdin SQL probe, real-tab case generation, and requested-database readiness checks.

Attempt 3 tested `d0e7cd4cba8f3bab4684fe01d017c7d2c0e4d354` and reached real PostgreSQL execution.

Attempt 3 results:

- schema succeeded in 521 ms;
- seed succeeded in 34,263 ms;
- seed inserted 1,000 users, 100,000 posts, 100,000 media rows, 100 tags, and 300,000 post_tags;
- four EXPLAIN plans completed;
- fifth around-post plan failed before HTTP benchmarking because the derived-table alias `window` is a PostgreSQL keyword in this context.

The same `window` alias existed in production `BuildAround`, so this exposed a real endpoint correctness bug, not merely a benchmark fixture issue.

Preserved server results include `/tmp/ginbar-m2-gate-20261001T212828Z-shared`.

## Attempt 3 measured query evidence

Baseline shapes before the current tuning:

- first feed page: 0.153 ms execution, 8 shared hits, 61 rows;
- old cursor (`id < 50000`): 11.326 ms, 1,246 hits; merge join scanned about 50,077 media rows for 61 output rows;
- required tag + score: 70.759 ms, 158,943 hits; `post_tags_pkey` produced about 158,680 rows and removed about 157,093 by join filter for 61 output rows;
- required + excluded tag fixture: 3.851 ms, 7,130 hits, but that old fixture omitted the score predicate and is not directly comparable to the real HTTP search case.

These measurements justify query-shape work before adding cache complexity or speculative indexes.

## Current measured-response changes

Implemented after attempt 3:

1. Around-post correctness:
   - renamed derived-table alias from reserved `window` to `combined_posts` in production query and benchmark SQL.

2. Media join work:
   - feed and around queries now use a bounded `JOIN LATERAL (... WHERE m.post_id = p.id ... LIMIT 1)` ready-media lookup.
   - goal: avoid the old-cursor merge join scanning tens of thousands of unrelated media rows.

3. Included-tag work:
   - included tags now use a tag-led `p.id IN (SELECT pt.post_id ... JOIN tags ...)` shape.
   - goal: let PostgreSQL use the existing partial `(tag_id, post_id DESC)` active-tag index instead of scanning `post_tags` in post-ID order.

4. No new database index was added. Existing indexes must be measured with the better relational/query shape first.

5. Benchmark fixtures now mirror the tuned production shapes and the required+excluded case includes `score >= 100`, matching the HTTP benchmark case.

6. `bench/explain_compare.sql` captures baseline and tuned first-page, old-cursor, required-tag+score, and required+excluded+score plans in the same seeded database. `run_gate.sh` records it as `explain-compare.txt` for direct before/after evidence.

## Checks after query changes

A local isolated Go 1.23.2 query-builder harness with production-equivalent feed/model/search types passed `go test ./...` for the changed `querysql` package and new shape assertions.

Real Go 1.25/pgx/PostgreSQL validation of the changed branch remains required; do not claim the measured improvements until the next target-server run.

## M2 gate status

M2 is not ready to merge into `v2`.

Still required:

- real Go 1.25 tests on the current tuned head;
- all five tuned `EXPLAIN (ANALYZE, BUFFERS)` plans;
- same-run baseline-vs-tuned plan comparison;
- complete HTTP matrix at concurrency 1 / 4 / 8 / 16 / 32;
- API CPU/RSS, server load, and PostgreSQL connection behavior;
- pool-size decision only if measured saturation justifies it.

Quiet-host Wallium comparison is optional and should be skipped unless stopping/restoring only backend/worker is clearly safe.

## Single best next task

Use the execution-only local agent to run `docs/v2/M2_RUN.md` from the current `origin/astra/m2-core-schema-api` head on the target server. Return the five tuned plans, `explain-compare.txt` before/after results, complete HTTP matrix, and resource/connection evidence. Do not modify code or indexes on the server. Use those measurements here to accept/reject the query-shape changes and decide whether any index change is actually needed before M2 integration.
