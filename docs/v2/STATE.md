# Ginbar v2 State / Handoff

Last updated: 2026-10-02
Phase: M3 durable media-job boundary IMPLEMENTED ON BRANCH; validation gate pending
Integration branch: `v2`
Active M3 branch: `astra/m3-media-job-boundary`
Completed M2 branch: `astra/m2-core-schema-api`
Completed M1 branch: `astra/m1-scroll-anchor`
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the resume point. Read it before `PLAN.md`. Do not rely on chat history as project memory.

## Branch status

- `master` remains untouched by rewrite work.
- `v2` remains at `c1c5f095d4a8cc342809381ec171df7ee99cb02d` at the start of M3 work.
- M1 completed at `32e154c4541fcdd2c24c1c05ed278e2dbddef960`.
- M2 passed its real Go/PostgreSQL/query/load/visibility gates and was fast-forwarded into `v2` at `ffbf529d88686aa123890893386aaf37dc1b3bb2` with no merge commit; follow-up state documentation brought `v2` to `c1c5f095d4a8cc342809381ec171df7ee99cb02d`.
- M3 durable media-job boundary implementation is on `astra/m3-media-job-boundary` at `b33d9eaba864809afd250c55ac5d335a133cef76` before this state-file update.
- Backend v2 lives under `src/backend/v2`; legacy Wallium backend is reference-only.
- Worker v2 work lives under `src/worker/v2`; legacy `src/worker` code is reference-only unless explicitly reviewed for reuse.
- `.local-agent-results/` is ignored by Git for local-agent evidence ZIPs.

## M2 architecture retained

- Go 1.25, pgx/v5 5.11.0, standard `net/http`;
- PostgreSQL authoritative for app data and durable job state;
- no Redis dependency without measured need;
- PostgreSQL pool cap 8;
- request/DB deadline 3 seconds;
- cursor pagination by post ID, never OFFSET;
- real search lexer/parser/AST;
- immutable numeric relational IDs; usernames never foreign keys;
- content filters are allowed-visibility constraints, not merely search context.

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

Lexer accepts `tag-42` while retaining `-tag` exclusion and negative score values. Real Go 1.25 tests and target-server search smoke pass.

## Around/direct-link semantics

Around reconstruction has three bounded branches:

1. newer context: allowed visibility + search, `id > selected`, limit radius;
2. selected: allowed visibility + release/deletion/media checks, ignores search predicates;
3. older context: allowed visibility + search, `id < selected`, limit radius.

At radius 30 the result is at most 61 posts.

This keeps an allowed canonical post visible regardless of surrounding search state without bypassing content visibility.

## Run 7 full shared-host gate — passed

Tested `1b74ae6e271c544847e7bbbc9e23c8a136007298` on Ubuntu 24.04 / i7-7700 4C/8T / 62 GiB RAM with Wallium running, Docker Go 1.25.14, PostgreSQL 17.11, and a fresh deterministic 100k-post DB.

Validation:

- `go mod tidy`: pass;
- `go test ./...`: pass;
- committed module files matched tidy output;
- all five SQL plans bounded;
- 25/25 HTTP cells at concurrency 1/4/8/16/32: 2,000 successes, zero errors;
- peak API CPU 51%, RSS 20,372 KiB;
- PostgreSQL reached 8 active connections in only 1/52 samples.

Decision: pool 8 is sufficient. Do not test/increase to 16 without new saturation evidence.

## Final visibility gate — passed

Tested final M2 code at `d9fafdaabff39bbb30f598b8f719e49be474666a` using Docker Go 1.25.14 and PostgreSQL 17.11.

Validation:

- full Go test suite passed;
- generated module files matched committed files;
- schema 138 ms; deterministic seed 32,491 ms;
- SFW post 49999: HTTP 200, selected exactly once, 61 SFW posts total;
- `49999?q=tag-42`: selected remains present exactly once while search shapes context;
- NSFW post 50000 under default SFW visibility: HTTP 404 `post_not_found`.

Around EXPLAIN:

- planning 1.981 ms; execution 0.465 ms; 195 shared hits;
- strict `id > 49999`, exact `id = 49999`, strict `id < 49999`;
- 30 + 1 + 30 bounded rows;
- all media access uses bounded `media_pkey` lookup;
- no substantial unexpected scan.

Around-only HTTP regression, 2,000 successes / zero errors per cell:

- c1 p95 2.277 ms / 557.8 rps;
- c4 p95 2.628 ms / 1726.8 rps;
- c8 p95 4.871 ms / 2448.5 rps;
- c16 p95 8.443 ms / 2527.0 rps;
- c32 p95 13.712 ms / 2718.6 rps.

Versus Run 7, p95 changed -0.022 to +0.920 ms and throughput -2.4% to -8.2%, with zero failed requests and no severe monotonic regression. Peak API CPU 34.9%, RSS 19,388 KiB, PG active connections 7.

Decision: visibility correction accepted; no additional M2 tuning justified.

## M2 gate decision

M2 PASSES AND IS INTEGRATED.

Keep:

- pool cap 8;
- bounded media lookup;
- resolved tag-ID search shape;
- current indexes;
- no Redis dependency.

No further M2 benchmark is justified by current evidence.

## M3 durable media-job boundary — implementation pending validation

Implementation commit before this state update: `b33d9eaba864809afd250c55ac5d335a133cef76` on `astra/m3-media-job-boundary`.

Implemented:

- `002_media_job_leases.sql` extends existing `media_jobs` rather than replacing it;
- explicit `lease_generation` fencing token increments on every successful claim/reclaim;
- running-state checks require attempt number, worker identity, claim timestamp and lease expiry;
- non-running states clear ownership/lease fields;
- one partial expression index, `media_jobs_runnable_idx`, orders runnable rows by readiness timestamp, then priority and ID;
- claim is one atomic PostgreSQL statement using `FOR UPDATE SKIP LOCKED` and returns at most one job;
- expired leases are reclaimed by the same bounded claim path;
- if the final allowed attempt dies, the next claim pass terminalizes it instead of starting attempt `max_attempts + 1`;
- complete, fail and renew transitions require worker ID + lease generation + unexpired lease, so stale workers cannot commit after reclaim;
- retryable failure returns to pending with caller-supplied backoff until the attempt limit; non-retryable failure is terminal;
- `src/worker/v2` is a clean-slate Rust crate using direct PostgreSQL access, not the legacy worker implementation;
- initial worker concurrency is exactly one claim at a time;
- no production polling loop exists yet, avoiding idle database polling before processors exist;
- `claim-once` is an explicit crash/recovery probe that claims one job and intentionally lets its lease expire;
- README makes the idempotency rule explicit: leases prevent concurrent ownership but cannot make external media side effects exactly-once, so processors must use deterministic/upsert/atomic-publication semantics.

Performance decision:

- runnable ordering is readiness timestamp first, then priority and ID. This aligns the claim predicate/order with one partial expression index and avoids a strict priority-first design where future-scheduled high-priority rows can cause avoidable scans. Priority is a tie-breaker among similarly ready work, not strict global preemption.

Tests added but not yet executed in an authoritative environment:

- `FOR UPDATE SKIP LOCKED` skips a locked candidate;
- expired lease reclaim increments attempt and fence generation;
- stale completion is rejected after reclaim;
- repeated crash at max attempts becomes terminal failure;
- retryable failure requeues and then terminalizes at max attempts;
- lease renewal/completion require current ownership;
- bounded exponential retry-delay unit test.

Validation status:

- branch/base relationship was verified: implementation commit is exactly one commit ahead of `v2` base `c1c5f095d4a8cc342809381ec171df7ee99cb02d`;
- this ChatGPT runtime has Go 1.23.2 but no `rustc`, `cargo`, or `psql`, and cannot reach GitHub from the shell;
- therefore Rust compile/format/lint, real PostgreSQL integration tests, claim/reclaim crash tests, and `EXPLAIN (ANALYZE, BUFFERS)` are still required;
- do not integrate this M3 slice into `v2` until that gate passes.

## Local-agent evidence workflow

Before every future local-agent handoff, ensure `.local-agent-results/` remains gitignored. Require one ZIP containing exact SHA/status, commands, stdout/stderr, logs, JSON/TSV/CSV, plans, benchmarks, environment/versions, errors, cleanup/restoration evidence, and `findings.md`. If execution is remote, the agent must download/copy the ZIP into `.local-agent-results/` and report the exact local path. The user should upload the ZIP here with: `Analyze this results ZIP, update project state, decide what the evidence means, and plan/implement the next step.`

## Single best next task

Run the execution-only M3 durable-job validation gate against the exact branch SHA after this state-file commit: Go tests, Rust fmt/check/clippy/tests, disposable PostgreSQL 17 state-machine/crash-reclaim tests, and `EXPLAIN (ANALYZE, BUFFERS)` plus a small claim-contention benchmark. Return one raw-evidence ZIP; analyze it here and fix the branch if any gate fails before considering fast-forward integration into `v2`.
