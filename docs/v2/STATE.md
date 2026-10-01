# Ginbar v2 State / Handoff

Last updated: 2026-10-01
Phase: M2 core schema/API implemented; real PostgreSQL plans captured; media-path fix validated; second tag-query shape and complete HTTP gate outstanding
Integration branch: `v2`
Active implementation branch: `astra/m2-core-schema-api`
Completed M1 branch: `astra/m1-scroll-anchor`
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the resume point. Read it before `PLAN.md`. Do not rely on chat history as project memory.

## Branch status

- `master` remains untouched by rewrite work.
- M1 is complete and integrated into `v2` at `32e154c4541fcdd2c24c1c05ed278e2dbddef960`.
- `astra/m2-core-schema-api` remains a clean descendant of that `v2` head; do not merge until the M2 gate passes.
- M2 contains the clean Go API/domain/search slice, fresh PostgreSQL schema/store/query compiler, real Go 1.25 dependency graph, deterministic load generator, and target-server benchmark harness.

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

Endpoints currently implemented:

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

Earlier execution-only runs fixed three benchmark-only issues:

1. Docker Go build output was not persisted to the host temporary directory; fixed with a bind-mounted `/out`.
2. Dockerized `psql -f <host-path>` could not access archived SQL files; fixed by streaming SQL through stdin.
3. Run 4 found the default API port 18080 already owned by `wise-old-bot-api-1`. The benchmark API failed to bind, but the runner accepted the unrelated listener's `/healthz` response before failing on `/api/v2/feed`.

Current harness response to issue 3:

- auto-selects a free loopback port from 18080-18179 unless explicitly configured;
- rejects an explicitly occupied port;
- verifies the spawned API PID is alive before accepting health;
- records the selected API port;
- supports `GINBAR_BENCH_PG_CONTAINER` so per-second PostgreSQL connection sampling uses `docker exec` into the existing disposable DB container instead of launching a new client container every second;
- excludes the sampler's own PostgreSQL session from connection counts.

The exact committed `run_gate.sh` content was syntax-checked locally with `bash -n` before commit; local Git blob hash matched committed blob `ea577bc8d6c64d2aaf9e34a4c93186c7daedc441`.

## Run 4 PostgreSQL evidence

Run 4 tested `0d1887300d697dcbfaa75cd397655b1f137478cc` on the target server.

Database preparation:

- schema: 381 ms;
- seed: 33,771 ms;
- 1,000 users;
- 100,000 posts;
- 100,000 media rows;
- 100 tags;
- 300,000 post_tags.

Largest relations:

- `post_tags`: 40 MB;
- `media`: 32 MB;
- `posts`: 16 MB.

All five tuned plans completed:

- first feed: 0.201 ms, 187 shared hits;
- old cursor: 0.208 ms, 189 hits;
- required tag + score: 71.329 ms, 158,943 hits;
- required + excluded tag + score: 69.353 ms, 159,675 hits;
- around post 50000: 0.256 ms, 189 hits.

Same-database baseline/tuned evidence for the bounded media lookup:

- old cursor: 18.447 ms / 1,246 hits -> 0.357 ms / 186 hits;
- about 51.7x faster in that EXPLAIN pair;
- old plan scanned about 50,077 media rows; tuned plan performed 61 `media_pkey` lookups;
- first-page pair changed 0.286 -> 0.321 ms, a 0.035 ms increase; both remain sub-millisecond.

Decision: keep the bounded per-post media lookup. It removes substantial cursor work with negligible measured first-page cost.

Around-post correctness is also validated: the reserved derived-table alias was fixed and the two-sided query completed in 0.256 ms.

## Tag-query evidence and current response

Run 4 rejected the first tag-led rewrite as ineffective.

Both required-tag plans still scanned about 158,680 `post_tags` rows for 61 results. The existing partial index:

`post_tags_tag_post_active_idx (tag_id, post_id DESC) WHERE removed_at IS NULL`

showed zero scans in the pre-HTTP index statistics; PostgreSQL instead scanned `post_tags_pkey` in post-ID order and applied the tag join afterward.

No new index is added yet.

Current query response:

- resolve each included normalized tag name to its unique numeric `tags.id` in a scalar subquery first;
- filter `post_tags.tag_id` directly with that resolved ID;
- retain `removed_at IS NULL`, making the predicate compatible with the existing partial tag/post index;
- keep the already-cheap excluded-tag per-post check unchanged.

The exact changed `querysql/feed.go` blob passed an isolated local Go test; local Git blob hash matched committed blob `6b0fc5d301006297d682e05d63d02bde72006d27`.

`bench/explain.sql` and `bench/explain_compare.sql` mirror the new production shape. The next server run must confirm whether PostgreSQL actually uses `post_tags_tag_post_active_idx` and collapses the prior ~158k-row scan before any schema/index change is considered.

## HTTP gate status

No valid HTTP matrix exists yet. Run 4 stopped at smoke testing because of the unrelated listener on port 18080; no API latency/resource conclusion should be drawn from that run.

Still required on the current head:

- real Go 1.25 tests/builds;
- five current tuned plans;
- same-run baseline/tuned plan comparison;
- complete HTTP matrix at concurrency 1 / 4 / 8 / 16 / 32;
- API CPU/RSS and server load;
- PostgreSQL total/active connection behavior;
- pool-size decision only if measured saturation justifies it.

Quiet-host Wallium comparison remains optional and should be skipped unless stopping/restoring only backend/worker is clearly safe.

## Single best next task

Use the execution-only local agent to run `docs/v2/M2_RUN.md` from the current exact `origin/astra/m2-core-schema-api` head. Pass `GINBAR_BENCH_PG_CONTAINER` as documented. Return the five current plans, `explain-compare.txt`, complete 5x5 HTTP matrix, resource/connection samples, and cleanup state. Do not modify code or indexes on the server. Use that evidence here to accept/reject the tag-ID query shape and decide whether M2 needs an index/schema change before integration.
