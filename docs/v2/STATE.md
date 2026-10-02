# Ginbar v2 State / Handoff

Last updated: 2026-10-02
Phase: M3 media pipeline integration — durable media-job boundary PASSED AND INTEGRATED
Integration branch: `v2`
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the resume point. Read it before `PLAN.md`. Do not rely on chat history as project memory.

## Branch status

- `master` remains untouched by rewrite work.
- `v2` was fast-forwarded from `c1c5f095d4a8cc342809381ec171df7ee99cb02d` through the validated M3 durable-job boundary at `23a41843503f70fbc1b471c9686bfb4602d16f71`, then received this state-only integration commit.
- M1 board benchmark is complete.
- M2 fresh schema + core Go API is complete and integrated.
- M3 is in progress. The durable PostgreSQL media-job ownership/recovery boundary is complete and integrated; upload/URL ingestion, real media processing, release workflow, regeneration, and progress UI remain.
- Backend v2 lives under `src/backend/v2`; legacy backend is reference-only.
- Worker v2 lives under `src/worker/v2`; legacy `src/worker` is reference-only unless explicitly reviewed for reuse.
- `.local-agent-results/` is ignored for local-agent evidence ZIPs.

## Retained M2 architecture and decisions

Keep these unless new evidence contradicts them:

- Go 1.25, pgx/v5, standard `net/http`;
- PostgreSQL authoritative for app data and durable job state;
- no Redis dependency without measured need;
- PostgreSQL pool cap 8;
- request/DB deadline 3 seconds;
- post-ID cursor pagination, never OFFSET;
- bounded media lookup;
- resolved immutable tag-ID search using existing indexes;
- real search lexer/parser/AST;
- immutable numeric relational IDs; usernames never foreign keys;
- content visibility is an allowed-visibility constraint, not merely search context.

M2 target-host validation remains accepted. Do not reopen pool/index/search decisions without new evidence.

## M3 durable media-job boundary — integrated

The first M3 slice establishes durable worker ownership/recovery before codecs or media processing.

Integrated architecture:

- PostgreSQL is the only durable media-job authority;
- existing `media_jobs` is extended rather than replaced;
- `lease_generation` is a fencing token incremented on every successful claim/reclaim;
- one atomic claim/reclaim statement uses `FOR UPDATE SKIP LOCKED` and returns at most one job;
- expired jobs are reclaimed through the same bounded claim path;
- final-attempt crashes terminalize on the next claim pass rather than creating attempt `max_attempts + 1`;
- retryable failures return to pending with delayed `available_at`; non-retryable/exhausted failures are terminal;
- non-running states clear claim/lease ownership fields;
- processing is explicitly at-least-once, so future durable media side effects must use deterministic keys/upserts or equivalent atomic publication;
- initial worker concurrency is one job at a time;
- no production polling loop and no Redis wakeup dependency exist yet.

Claim ordering is readiness timestamp first, then priority and ID. `media_jobs_runnable_idx` matches that order. Priority is a tie-breaker among similarly ready work rather than strict global preemption.

Lifecycle correctness details:

- renew/complete/fail first lock the exact owned row via a materialized `SELECT ... FOR UPDATE` CTE and evaluate expiry afterward with `clock_timestamp()`;
- a lifecycle operation that starts before expiry but waits on a row lock past expiry is rejected;
- `claim_one` and `renew_lease` take positive `NonZeroU64` lease milliseconds, eliminating zero/sub-millisecond lease construction;
- generation fencing rejects stale/wrong owners after reclaim;
- no database transaction spans media processing.

## M3 validation history

### First gate — implementation defects found

Evidence ZIP: `m3-media-job-boundary-20261002T222824Z.zip`.
Exact tested SHA: `c015fec556ed21b588d05c6c8371592addda7a49`.

Found and fixed:

- lifecycle mutations used transaction-start `now()` and could wait past lease expiry yet still commit;
- lease durations could truncate zero/sub-millisecond `Duration` values to zero;
- Rust formatting and Clippy checks failed.

The same gate established that the claim path was already bounded and index-backed: the 100k mixed-queue runnable claim used `media_jobs_runnable_idx` and executed in about 0.564 ms; fresh idle lookup used three shared hits / about 0.222 ms. Target-host c1/c4 contention completed with zero errors.

One immediately post-churn idle lookup touched 51,562 shared buffers. Treat this as MVCC/index-cleanup sensitivity evidence, not justification for a different claim query/index or Redis. There is still no production polling loop.

### Second gate — implementation passed; committed fixture bug found

Evidence ZIP: `m3-media-job-boundary-revalidation-20261002T210832Z.zip`.
Exact tested SHA: `ddef21300c4b8f4fbe8ecd1d5ca0bbc5dc5359fb`.
Environment: Rust 1.99.0, PostgreSQL 17.11, Docker 28.5.1 on the target host.

Passed:

- Rust fmt/check/Clippy/unit checks;
- independent lock-wait expiry traces for complete/renew/fail;
- positive `NonZeroU64` lease API checks;
- SKIP LOCKED, single-owner race, crash/reclaim, retry, max-attempt, stale/wrong owner/generation, ownership clearing, and claim-once probes;
- runnable 100k-job plan about 0.577 ms and fresh-idle plan about 0.215 ms;
- c1: 1,000 transactions, zero errors, 2.681 ms average, 373.05 TPS;
- c4: 4,000 transactions, zero errors, 2.744 ms average, 1457.68 TPS;
- no material contention regression.

The only gate failure was the committed lock-wait regression fixture: after rejected completion, `claim_one` correctly reclaimed the same expired job while the fixture incorrectly locked a newly inserted job. Production behavior itself passed the independent traces.

Fixture correction `7526bc60628c2dc68ccbabee12ed0b4be92aebeb` reuses the same job and asserts attempts/generations 1/1 -> 2/2 -> 3/3 so every lock targets the actual leased row.

### Final targeted gate — PASS

Evidence ZIP: `m3-media-job-boundary-final-20261002T214437Z.zip`.
Exact tested SHA: `23a41843503f70fbc1b471c9686bfb4602d16f71`.
Uploaded ZIP SHA-256: `C641408284F0AC7A9D5859998048EADAC21E43AAA1C758FC34EF6EA4CA19EF2A`.

Verified from raw evidence:

- exact SHA and merge-base were correct; checkout was clean;
- diff since `ddef21300c4b8f4fbe8ecd1d5ca0bbc5dc5359fb` changed only `src/worker/v2/tests/job_state.rs` and `docs/v2/STATE.md`; production Rust/SQL/Go/manifests were unchanged;
- `cargo fmt --check`, `cargo check --all-targets`, Clippy `-D warnings`, and no-DB tests all passed;
- full PostgreSQL 17.11 suite passed: five committed DB integration tests, zero failures;
- `mutations_recheck_expiry_after_row_lock_wait` passed 10/10 focused runs with zero failures, skips, timeouts, or observed flakes;
- every focused run used clean disposable DB state and exercised one job through attempts/generations 1/1 -> 2/2 -> 3/3;
- cleanup removed disposable DB/container/build/cache resources and the tested checkout was clean before disposal;
- expensive claim-plan/contention benchmarks were intentionally not rerun because production code had not changed since the already-passed second gate.

Decision: durable media-job ownership/recovery boundary PASSES and is integrated into `v2`. No further tuning of this slice is justified by current evidence.

## Remaining non-blocking observations

- Immediately post-churn idle lookup cost remains MVCC/index-cleanup sensitive even though fresh/steady lookups are bounded and index-backed. Revisit only when the production worker polling/wakeup mechanism exists and can be measured.
- Extremely large positive lease millisecond values can exceed PostgreSQL timestamp/interval range and return a database error. Ordinary configured leases are unaffected; define a practical upper bound with the production worker-loop configuration instead of adding an arbitrary limit now.

## M3 next-slice constraints

The next media work should preserve these boundaries:

- no anonymous posting path;
- keep HTTP handlers thin and put ingestion/workflow logic behind explicit service/storage boundaries;
- unreleased/processing posts must stay invisible to the public feed;
- PostgreSQL remains authoritative for workflow/job state;
- local NVMe is the intended media store;
- stage inputs with bounded size/time/concurrency and cancellation;
- avoid holding database transactions while streaming/downloading bytes;
- enqueue durable processing work transactionally with the authoritative post/source state;
- do not introduce Redis unless a measured wakeup/cache need appears;
- do not start codec optimization before the source-ingestion/storage contract is coherent.

## Local-agent evidence workflow

For target-host work, use isolated/disposable resources and return one ZIP with exact SHA/status, commands, raw stdout/stderr, environment/tool versions, measurements, failures, and cleanup/restoration evidence. Preserve raw remote evidence until no longer needed.

## Single best next task

Start the next M3 slice from current `v2`: design and implement the upload/URL-ingestion + durable-enqueue boundary before codecs. Define the minimal staging/source-storage metadata needed in the fresh schema, keep byte streaming/downloads outside DB transactions with explicit size/time/cancellation limits, create the unreleased post/source state and processing job atomically once the staged input is accepted, and keep the service boundary compatible with future authenticated user context rather than exposing anonymous posting. Validate DB/file cleanup on failures and cancellation, duplicate/partial-ingestion behavior, bounded resource use, and the hot SQL shapes before beginning AVIF/video processing.
