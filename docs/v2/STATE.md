# Ginbar v2 State / Handoff

Last updated: 2026-10-02
Phase: M3 durable media-job boundary corrected; final committed-test revalidation pending
Integration branch: `v2`
Active M3 branch: `astra/m3-media-job-boundary`
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the resume point. Read it before `PLAN.md`. Do not rely on chat history as project memory.

## Branch status

- `master` remains untouched by rewrite work.
- `v2` remains at `c1c5f095d4a8cc342809381ec171df7ee99cb02d`.
- M1 board benchmark is complete.
- M2 fresh schema + core Go API is complete and integrated.
- M3 durable media-job boundary is isolated on `astra/m3-media-job-boundary` and is not yet integrated.
- Production-code correction for the first failed M3 gate landed before `ddef21300c4b8f4fbe8ecd1d5ca0bbc5dc5359fb`.
- The second gate found only a committed regression-fixture bug; the fixture correction is `7526bc60628c2dc68ccbabee12ed0b4be92aebeb` before this state-file update.
- Backend v2 lives under `src/backend/v2`; legacy backend is reference-only.
- Worker v2 lives under `src/worker/v2`; legacy `src/worker` is reference-only unless explicitly reviewed for reuse.
- `.local-agent-results/` is ignored on the M3 branch for evidence ZIPs.

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

## M3 durable media-job boundary architecture

The first M3 slice establishes durable ownership/recovery before codecs or media processing.

Keep:

- PostgreSQL as the only durable job authority;
- existing `media_jobs` extended rather than replaced;
- `lease_generation` as the fencing token, incremented on every successful claim/reclaim;
- one atomic claim/reclaim statement using `FOR UPDATE SKIP LOCKED`, at most one job, with short transaction scope;
- expired jobs reclaimed through the same claim path;
- final-attempt crashes terminalized on the next claim pass rather than creating attempt `max_attempts + 1`;
- retryable failures returned to pending with delayed `available_at`; non-retryable/exhausted failures terminal;
- non-running jobs clearing ownership/lease fields;
- at-least-once processing semantics: future durable side effects must use deterministic keys/upserts or equivalent atomic publication;
- initial worker concurrency of one job at a time;
- no production polling loop and no Redis wakeup dependency yet.

Claim ordering remains readiness timestamp first, then priority and ID. `media_jobs_runnable_idx` matches that order. Priority is a tie-breaker among similarly ready work rather than strict global preemption.

## First M3 gate — FAILED on implementation defects

Evidence ZIP: `m3-media-job-boundary-20261002T222824Z.zip`.
Tested SHA: `c015fec556ed21b588d05c6c8371592addda7a49`.

Important failures found:

- lifecycle mutations used transaction-start `now()` and could wait on a row lock past lease expiry yet still commit;
- public lease durations accepted zero/sub-millisecond `Duration` values that truncated to zero;
- `cargo fmt --check` and Clippy `-D warnings` failed.

The same gate also established that the claim path itself was healthy: `media_jobs_runnable_idx` was used, the 100k mixed-queue runnable claim executed in about 0.564 ms, fresh idle lookup used three shared hits / about 0.222 ms, and target-host c1/c4 contention completed with zero errors. One immediately post-churn idle lookup touched 51,562 buffers; treat that as MVCC/index-cleanup sensitivity evidence, not justification for a new query/index/Redis design.

## Production-code correction

The implementation was corrected before the second gate:

- renew/complete/fail first lock the exact owned row in a materialized `SELECT ... FOR UPDATE` CTE, then evaluate expiry with `clock_timestamp()`;
- claim timestamps/deadlines use real post-selection time;
- `claim_one` and `renew_lease` take positive `NonZeroU64` milliseconds, eliminating zero/sub-millisecond lease construction;
- generation fencing, max-attempt behavior, readiness-first ordering, and the existing claim index remain unchanged;
- fmt/Clippy issues were corrected.

## Second M3 gate — implementation PASS, committed fixture FAIL

Evidence ZIP: `m3-media-job-boundary-revalidation-20261002T210832Z.zip`.
Exact tested SHA: `ddef21300c4b8f4fbe8ecd1d5ca0bbc5dc5359fb`.
Target environment: Go 1.25.14, Rust 1.99.0, PostgreSQL 17.11, Docker 28.5.1 on the target host.

Passed:

- exact SHA/base/clean-worktree checks and branch ignore rule;
- Go tests;
- `cargo fmt --check`, `cargo check --all-targets`, Clippy `-D warnings`, and no-DB unit checks;
- independent lock-wait traces for complete/renew/fail: each started unexpired, blocked through real expiry, rejected afterward, and left the full durable row unchanged;
- positive lease API checks: `NonZeroU64` required, zero/sub-ms `Duration` calls fail to compile;
- SKIP LOCKED, single-owner race, reclaim, retries, max-attempt handling, stale/wrong owner and generation rejection, ownership clearing, and claim-once crash/reclaim probes;
- runnable 100k-job plan remained index-backed/bounded: about 0.577 ms;
- fresh idle 100k-job plan: three shared hits / about 0.215 ms;
- c1 benchmark: 1,000 transactions, zero errors, 2.681 ms average, 373.05 TPS;
- c4 benchmark: 4,000 transactions, zero errors, 2.744 ms average, 1457.68 TPS;
- no material contention regression versus the first gate;
- cleanup/restoration and evidence packaging.

The gate still reported FAIL because the committed `mutations_recheck_expiry_after_row_lock_wait` fixture was incorrect. After rejected completion, the first job remained running with an expired lease. The fixture inserted a second job, but the next `claim_one` correctly reclaimed the earlier expired job while the test locked the newly inserted row. The mutation therefore did not block on the row the test expected. External traces proved the implementation behavior itself was correct.

## Regression-fixture correction

Commit `7526bc60628c2dc68ccbabee12ed0b4be92aebeb` fixes only `src/worker/v2/tests/job_state.rs`:

- one job is intentionally reused for the three lock-wait phases;
- completion uses attempt/generation 1/1;
- renewal explicitly reclaims the same job and asserts 2/2;
- failure explicitly reclaims the same job and asserts 3/3;
- every row lock and expiry observation now targets the actual leased job ID.

No production Rust or SQL changed after the second gate. Therefore repeating the 100k-job plans or contention benchmark would be redundant unless the focused revalidation reveals a new production-code issue.

## Remaining non-blocking observations

- Immediately post-churn idle lookup cost remains MVCC/index-cleanup sensitive even though fresh/steady lookups are bounded and index-backed. There is still no production polling loop, so defer polling cadence/observability decisions until the real worker loop exists.
- Extremely large positive lease millisecond values can still exceed PostgreSQL timestamp/interval range and yield a database error. Ordinary configured leases are unaffected; choose a practical configuration upper bound when the production worker loop/config contract is introduced rather than adding an arbitrary limit now.

## Local-agent evidence workflow

Before future local-agent handoffs, ensure `.local-agent-results/` is ignored on the exact tested checkout. Require one ZIP with exact SHA/status, commands, stdout/stderr, environment/tool versions, failures, and cleanup/restoration evidence. For target-host work, copy the ZIP back into `.local-agent-results/` and report its exact local path. Preserve raw remote evidence until no longer needed.

## Single best next task

Run a targeted execution-only revalidation against the exact M3 branch SHA after this state-file commit: verify exact SHA/base/clean checkout, run Rust fmt/check/clippy, run the full PostgreSQL-backed Rust test suite with special attention to `mutations_recheck_expiry_after_row_lock_wait`, repeat that focused regression several times to catch fixture timing flakiness, and confirm the diff since `ddef21300c4b8f4fbe8ecd1d5ca0bbc5dc5359fb` changes only the test fixture plus this state document. If all checks pass, return one evidence ZIP for final review and fast-forward integration into `v2`; do not rerun the already-passed 100k-job plans/contention benchmark unless production code changes again.
