# Ginbar v2 State / Handoff

Last updated: 2026-10-02
Phase: M3 durable media-job boundary FIXED AFTER FAILED GATE; revalidation pending
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
- M3 durable media-job boundary was first gated at `c015fec556ed21b588d05c6c8371592addda7a49`; that gate failed on a lease-expiry lock-wait race plus fmt/Clippy issues.
- Corrective implementation before this state update is `7483eec899f49531ed7c42d8dcd75f98419bc27a` on `astra/m3-media-job-boundary`.
- Backend v2 lives under `src/backend/v2`; legacy backend is reference-only.
- Worker v2 lives under `src/worker/v2`; legacy `src/worker` is reference-only unless explicitly reviewed for reuse.
- `.local-agent-results/` is intended for local-agent evidence ZIPs and must remain ignored before future handoffs.

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

Validated M2 target-host evidence included all 25 HTTP benchmark cells with 2,000 successes / zero errors per cell, peak API CPU about 51%, peak RSS about 20 MiB, and PostgreSQL reaching 8 active connections in only 1/52 samples. Pool 8 remains sufficient; do not test/increase to 16 without new saturation evidence.

Around/direct-link reconstruction remains three bounded branches: newer context with visibility + search, exact selected post with visibility/release/media checks but ignoring search predicates, and older context with visibility + search. Radius 30 returns at most 61 posts. Final visibility regression passed, including SFW canonical selection, search-independent selected-post retention, default NSFW hiding, bounded media lookups, and about 0.465 ms around-query execution.

## M3 durable media-job boundary

The first M3 slice establishes job ownership/recovery before codecs or media processing.

Architecture retained:

- PostgreSQL is the only durable job authority;
- existing `media_jobs` is extended rather than replaced;
- `lease_generation` is the fencing token and increments on every successful claim/reclaim;
- claim/reclaim is one atomic statement with `FOR UPDATE SKIP LOCKED`, at most one job, and short transaction scope;
- expired jobs are reclaimed through the claim path;
- final-attempt crashes terminalize on the next claim pass rather than creating attempt `max_attempts + 1`;
- retryable failures return to pending with delayed `available_at`; non-retryable or exhausted failures are terminal;
- non-running jobs clear lease ownership fields;
- processing is at-least-once: future durable media side effects must use deterministic keys/upserts or equivalent atomic publication;
- initial worker concurrency is one job at a time;
- no production polling loop and no Redis wakeup dependency yet.

Claim ordering remains readiness timestamp first, then priority and ID. `media_jobs_runnable_idx` matches that order. Priority is a tie-breaker among similarly ready work rather than strict global preemption.

## Initial M3 gate — FAILED, evidence retained

Raw evidence ZIP: `m3-media-job-boundary-20261002T222824Z.zip`.

Exact tested SHA: `c015fec556ed21b588d05c6c8371592addda7a49`.
Target environment: Go 1.25.14, Rust 1.99.0, PostgreSQL 17.11, Docker 28.5.1 on the target host.

Passed:

- exact SHA/base and clean-worktree checks;
- Go `go test ./...`;
- Rust `cargo check --all-targets` and unit tests;
- committed PostgreSQL integration tests;
- supplemental SKIP LOCKED, single-owner, crash/reclaim, retry, max-attempt, stale-generation, wrong-owner/generation, renewal/completion, and non-retryable-failure checks;
- `claim-once` real-expiry reclaim: same job moved from worker A attempt/generation 1/1 to worker B 2/2;
- mixed 100k-job claim plan used `media_jobs_runnable_idx`, selected one candidate, and executed in 0.564 ms;
- fresh 100k-job idle lookup: 3 shared hits, 0.222 ms;
- target-host contention: c1 = 1,000/1,000, zero errors, 2.549 ms average, 392.30 TPS; c4 = 4,000/4,000, zero errors, 3.119 ms average, 1282.66 TPS;
- cleanup/restoration and raw-evidence packaging.

Failed / important findings:

- renew/complete/fail used `lease_expires_at > now()`; PostgreSQL `now()` is fixed at transaction start, so a mutation could start before expiry, block on a row lock past expiry, and still succeed;
- renewal reproduced success with an already-expired resulting deadline after the lock wait;
- public `JobStore` lease durations accepted zero/sub-millisecond `Duration` values that truncated to zero and consumed an attempt/generation with an immediately expired lease;
- `cargo fmt --check` failed on formatting differences;
- Clippy `-D warnings` failed on unused `STATE_SUCCEEDED`;
- immediately after mass queue churn, one idle claim lookup touched 51,562 shared buffers and took 16.416 ms; the immediate repeat was 3 hits / 0.224 ms and a fresh idle DB was 3 hits / 0.222 ms. Treat this as MVCC/index-cleanup sensitivity evidence, not as justification for a new index/query or Redis/polling design.

## Corrective implementation

Corrective code before this state update: `7483eec899f49531ed7c42d8dcd75f98419bc27a` plus preceding call-site commit `5b4aab4e14253a8a06324f8ec2b6c1e9ce8d1352`.

Changes:

- renew/complete/fail now first acquire the exact owned row via a materialized `SELECT ... FOR UPDATE` CTE, then evaluate expiry with `clock_timestamp()`; a row-lock wait crossing expiry must therefore reject the transition;
- claim timestamps and lease deadline use `clock_timestamp()` after the SKIP LOCKED candidate is acquired; the readiness predicate/order remains unchanged and indexable;
- `JobStore::claim_one` and `renew_lease` now accept `NonZeroU64` lease milliseconds, making zero/sub-millisecond leases unrepresentable;
- integration regression coverage deliberately blocks complete/renew/fail while each lease is still live, waits for actual expiry, releases the row lock, and requires rejection;
- existing tests use the positive-millisecond lease API;
- formatting from the failed gate was normalized;
- `STATE_SUCCEEDED` is test-only to avoid the dead-code Clippy failure;
- worker README documents post-lock expiry semantics and the positive-millisecond lease API.

Decision: keep the readiness-first claim query/index and no Redis dependency. Normal runnable/fresh-idle behavior is bounded and index-backed; the one churn-sensitive lookup should inform future polling/observability design, but no polling loop exists yet.

## Validation status

The corrective code has not been compiled or run in this ChatGPT runtime because Rust/PostgreSQL toolchains are unavailable here. Do not fast-forward into `v2` until the corrected exact branch SHA passes:

- Go tests;
- Rust fmt/check/clippy/unit tests;
- PostgreSQL integration tests including the new row-lock-crosses-expiry regression;
- crash/reclaim probes and existing fencing/retry semantics;
- the same 100k-job runnable/fresh-idle plans;
- c1/c4 contention comparison against the recorded baseline.

## Local-agent evidence workflow

Before every future local-agent handoff, ensure `.local-agent-results/` is ignored. Require one ZIP with exact SHA/status, commands, stdout/stderr, logs, JSON/TSV/CSV, query plans, benchmark results, environment/versions, failures, cleanup/restoration evidence, and `findings.md`. Remote runs must copy the final ZIP into `.local-agent-results/` and report the exact local path. Preserve raw remote evidence until the user confirms it is no longer needed.

## Single best next task

Re-run the M3 execution-only gate against the exact branch SHA after this state-file commit, concentrating on the corrected lock-wait expiry regression and Rust fmt/Clippy while retaining the existing crash/reclaim/state-machine checks and the same 100k-job claim-plan + c1/c4 contention benchmark for regression comparison. Return one raw-evidence ZIP; if every gate passes, review and fast-forward the branch into `v2`.
