# Ginbar v2 State / Handoff

Last updated: 2026-10-02
Phase: M2 full shared-host performance gate passed; targeted around-post visibility regression outstanding
Integration branch: `v2`
Active implementation branch: `astra/m2-core-schema-api`
Completed M1 branch: `astra/m1-scroll-anchor`
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the resume point. Read it before `PLAN.md`. Do not rely on chat history as project memory.

## Branch status

- `master` remains untouched by rewrite work.
- M1 is complete in `v2` at `32e154c4541fcdd2c24c1c05ed278e2dbddef960`.
- M2 remains a clean descendant of that head and must not merge until the final targeted visibility gate passes.
- Backend v2 lives under `src/backend/v2`; legacy Wallium backend is reference-only.

## M2 architecture

- Go 1.25, pgx/v5 5.11.0, standard `net/http`;
- PostgreSQL authoritative for app data and durable job state;
- no Redis dependency without measured need;
- default PostgreSQL pool cap 8;
- request/DB deadline 3 seconds;
- cursor pagination by post ID, never OFFSET;
- real search lexer/parser/AST;
- immutable numeric relational IDs; usernames never foreign keys.

Endpoints:

- `GET /healthz`
- `GET /api/v2/feed?before=<id>&limit=<n>&q=<search>`
- `GET /api/v2/posts/<id>/around?radius=<n>&q=<search>`

## Validated query decisions

### Bounded media lookup — keep

Old cursor originally scanned about 50k media rows. Tuned shape performs one `media_pkey` lookup per returned candidate.

Run 5 same-run old cursor: 9.568 -> 0.156 ms. Run 7 old cursor remained 0.196 ms.

### Resolved tag-ID include filter — keep

Resolve normalized tag to immutable `tags.id`, then constrain active `post_tags.tag_id`.

Existing index:

`post_tags_tag_post_active_idx (tag_id, post_id DESC) WHERE removed_at IS NULL`

Run 5 same-run:

- tag + score: 71.240 -> 1.563 ms;
- tag + excluded + score: 70.068 -> 1.899 ms;
- included assignment scan: about 158,680 -> 1,587 rows.

Run 7 retained the intended plans. No additional search index justified.

### Hyphenated tags — fixed

Lexer now accepts `tag-42` while retaining `-tag` exclusion and negative score values. Real Go 1.25 tests and target-server search smoke pass.

## Run 7 full shared-host gate — passed

Tested revision: `1b74ae6e271c544847e7bbbc9e23c8a136007298`.

Target:

- Ubuntu 24.04;
- i7-7700, 4C/8T;
- 62 GiB RAM;
- Docker Go 1.25.14;
- disposable PostgreSQL 17.11;
- Wallium kept running.

Validation:

- `go mod tidy`: pass;
- `go test ./...`: pass;
- committed `go.mod` / `go.sum` byte-identical to tidy output;
- schema: 495 ms;
- deterministic seed: 33,562 ms;
- 100k posts, 100k media, 300k post_tags.

Plans:

- first feed: 0.194 ms;
- old cursor: 0.196 ms;
- tag + score: 2.952 ms;
- tag + excluded + score: 1.852 ms;
- around: 0.355 ms.

All remained bounded and used intended indexes.

HTTP matrix: 5 endpoints x concurrency 1/4/8/16/32, 2,000 requests per cell.

- every one of 25 cells: 2,000 successes, zero errors;
- no timeouts/non-200 benchmark responses;
- feed p95 at c32: 7.273 ms first page / 7.181 ms old cursor;
- tag+score p95 c32: 13.020 ms;
- tag+exclude+score p95 c32: 19.289 ms;
- around p95 c32: 13.396 ms.

Resources:

- peak API CPU: 51%;
- peak API RSS: 20,372 KiB;
- load1: 2.52-3.52;
- max PostgreSQL connections: 8 total / 8 active;
- 8 active connections occurred in only 1 of 52 samples (1.92%).

Decision: pool 8 is sufficient for the measured shared-host workload. Do not test/increase to 16 without new saturation evidence.

## Visibility semantic issue found after Run 7

`M2.md` defines `feed.Query.Filters` / `AroundQuery.Filters` as allowed content visibility. The unauthenticated skeleton defaults to SFW so non-SFW/secret content is not exposed before authenticated visibility exists.

Run 7's around implementation incorrectly let the selected canonical post bypass those filters. This made NSFW seed post 50000 visible by direct link despite default SFW visibility.

Correct semantics:

- newer/older context branches: allowed visibility + search predicates;
- selected branch: allowed visibility, but search predicates do not hide the canonical selected post;
- release/deletion/media-readiness constraints always apply.

Current `BuildAround` implements that split. `Filters` are now explicitly documented as allowed visibility.

Regression test asserts:

- strict `>` / `<` context bounds;
- visibility predicate applies to all three branches;
- search predicates apply only to newer/older;
- all three media lookups remain bounded;
- response remains at most 30 + selected + 30.

An isolated local Go 1.23 query-builder test passed after this correction.

## Final targeted gate prepared

Full Run 7 feed/search/pool evidence remains valid; only the corrected around branch needs revalidation.

Prepared artifacts:

- `src/backend/v2/bench/explain_visibility.sql`
- `src/backend/v2/bench/run_visibility_gate.sh`
- updated `docs/v2/M2_RUN.md`

The targeted gate:

- uses real Docker Go 1.25;
- applies fresh schema + seed to disposable PostgreSQL;
- verifies SFW post 49999 returns 200 and appears exactly once;
- verifies the selected post remains present even with nonmatching surrounding search state;
- verifies NSFW post 50000 returns 404 under default SFW visibility;
- captures corrected around EXPLAIN;
- benchmarks around post 49999 at concurrency 1/4/8/16/32 with 2,000 requests per cell;
- records CPU/RSS/PostgreSQL connection samples;
- never writes the repository.

## M2 integration gate

If the targeted visibility gate passes and around latency shows no material regression from Run 7, M2 is ready to integrate into `v2` with:

- pool cap remaining 8;
- current media lookup;
- current tag-ID search shape;
- current indexes;
- no Redis addition.

## Single best next task

Give an execution-only local agent the current exact `astra/m2-core-schema-api` head and have it run `docs/v2/M2_RUN.md` / `src/backend/v2/bench/run_visibility_gate.sh` against a fresh disposable PostgreSQL container. Return the smoke assertions, visibility EXPLAIN, five around latency cells, resource samples, clean-worktree state, and cleanup confirmation. Do not let the agent modify code or Git. If it passes, integrate M2 into `v2` here.
