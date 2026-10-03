# Ginbar v2 state / handoff

Last updated: 2026-10-03
Phase: **M3 media pipeline — production worker runner implemented on feature branch; correctness/performance gates pending**
Integration branch: `v2`
Legacy branch: `master` (read-only for rewrite work)

Read this file first. Use [`PLAN.md`](PLAN.md) for stable architecture/milestone rules and [`PERFORMANCE.md`](PERFORMANCE.md) for accepted benchmark history. Do not use chat history as project memory.

## Current refs / branch safety

Validated before this slice:

- `v2`: `a95dc4d4fa5647626b767c610be10325f1521dcb`;
- legacy `master`: `181fa44d79c7b4a1984c1a35795762dd503b3f77`;
- active feature branch: `astra/m3-worker-runner`.

`master` must not be modified. The worker-runner branch is not accepted into `v2` until its PostgreSQL correctness gate and target-host continuous-runner performance gate both pass.

## Current rewrite status

- M1 board performance prototype: **complete and integrated**.
- M2 fresh PostgreSQL schema + core Go API: **complete and integrated**.
- M3 durable PostgreSQL media-job ownership/retry/recovery: **integrated**.
- M3 upload/URL ingestion + durable enqueue: **integrated**.
- M3 source verification + generation-fenced DB publication: **integrated**.
- M3 still-image JPEG/PNG/WebP -> deterministic AVIF canonical + thumbnail pipeline: **integrated**.
- M3 production worker runner: **implemented on `astra/m3-worker-runner`, not yet validated/accepted**.
- M3 is **not complete**.

Do not start video before the production-runner boundary is accepted.

## Production runner slice — implementation pending validation

Current feature implementation adds:

- long-running `run` worker command;
- serial claiming with at most one executing job;
- 500 ms default idle polling rather than a busy loop;
- bounded PostgreSQL reconnect backoff (250 ms base, 5 s cap);
- existing durable job retry policy retained at 2 s base / 30 s cap;
- an independent lease-renewal thread and PostgreSQL connection while work is active;
- renewal at one third of the configured lease interval;
- bounded renewal reconnect/query retry with explicit `LeaseLost` versus repeated-renewal-failure classification;
- cancellation state visible to source verification and at processing/publication boundaries;
- graceful SIGINT/SIGTERM shutdown that stops new claims and attempts to requeue a still-owned active job;
- runner tests for idle wait behavior, repeated processing, one-active-job enforcement, long-job renewal, renewal failure, generation loss, restart/reclaim, and active shutdown.

Important cancellation limitation: processing-v1 AVIF encode and filesystem publication remain synchronous. A codec/filesystem call may finish after ownership or shutdown is detected. The runner therefore keeps renewal independent while work is active, checks cancellation at meaningful boundaries, and relies on the existing owner/generation/source/lease-fenced PostgreSQL publication as the authoritative commit point. Deterministic no-overwrite files created before a lost-ownership check remain safe for idempotent retry. Do not claim mid-codec interruption.

No Redis, scheduler weights, CPU quotas, cpusets, load admission, extra worker concurrency, or extra AVIF threads were added.

### Validation status

No correctness or performance result is accepted yet for this feature branch in this state file.

Required before acceptance:

- Rust/Cargo 1.99.x;
- `cargo fmt --manifest-path src/worker/v2/Cargo.toml -- --check`;
- `cargo check --locked --manifest-path src/worker/v2/Cargo.toml`;
- `cargo test --locked --manifest-path src/worker/v2/Cargo.toml` with `GINBAR_TEST_DATABASE_URL` set so PostgreSQL tests actually execute;
- `cargo clippy --locked --manifest-path src/worker/v2/Cargo.toml --all-targets -- -D warnings`;
- verify `src/worker/v2/Cargo.lock` is byte-identical to the integrated lockfile;
- inspect the complete feature diff against `v2` and verify no legacy/unrelated changes;
- target-host continuous-runner API coexistence/idle-overhead benchmark.

## Source boundaries

- `src/frontend/` — accepted SolidJS + TypeScript + Vite board/frontend prototype.
- `src/backend/v2/` — clean v2 Go API/schema/search/bench code.
- `src/worker/v2/` — clean v2 Rust worker and media pipeline.
- adjacent non-v2 source remains legacy/reference-only.

## Retained architecture / invariants

Keep unless new evidence justifies a change:

- Go 1.25 + standard `net/http` + pgx/v5;
- PostgreSQL authoritative for app state and durable media jobs;
- no Redis baseline dependency without measured need;
- Rust media worker;
- local NVMe media storage;
- immutable numeric relational IDs;
- post-ID cursor pagination, never OFFSET;
- real search lexer/parser/AST and parameterized SQL;
- durable jobs use PostgreSQL claim ownership + generation fencing;
- codec/filesystem work stays outside DB transactions;
- publication remains fenced by owner/generation, source identity, post eligibility, and lease validity;
- at-least-once work requires deterministic/idempotent side effects;
- current still-image concurrency = **one job at a time**;
- AVIF encoder threads = **one**;
- any output-affecting image change requires incrementing `PROCESSING_VERSION`.

## Integrated still-image contract

Processing version 1 currently supports JPEG, non-animated PNG, and non-animated WebP. Animated PNG/WebP, GIF, AVIF/HEIF input, and video remain terminal/out of scope for this processing version.

Limits/output:

- max source dimension: 16,384 px;
- max decoded pixels: 80,000,000;
- decoder allocation hint: 384 MiB;
- canonical max dimension: 1,280 px without upscale;
- thumbnail: 256x256 center crop;
- `ravif` speed 10;
- main quality 75;
- thumbnail quality 60;
- one encoder thread;
- deterministic canonical `.avif` and `<stem>.thumb.avif`;
- same-directory staged write + fsync + no-overwrite hard-link publication;
- exact size/SHA verification for idempotent reuse;
- conflicting existing destination bytes are terminal and never overwritten.

The accepted application lockfile is committed at `src/worker/v2/Cargo.lock`; worker validation must use `--locked` and the runner slice must not change those bytes.

## Current accepted performance decisions

Stable measurements remain in [`PERFORMANCE.md`](PERFORMANCE.md). Immediate reference points for the runner gate:

- M2 around-post p95: 2.277 ms at c1, 2.628 ms at c4, 4.871 ms at c8, 8.443 ms at c16, 13.712 ms at c32;
- prior single-image overlap p95 deltas: c1 +4.21%, c2 +2.78%, c4 +12.12%, c8 +26.22%;
- prior c8 loaded absolute p95: 5.888 ms;
- prior c8 throughput delta: -12.65%;
- accepted image configuration remains one job / one AVIF encoder thread;
- do not add scheduler/load-admission complexity until the real continuous runner is measured;
- there is no apples-to-apples global v1-v2 benchmark yet.

The pending gate must measure the real continuous runner, including idle CPU/query rate and lease-renewal overhead, rather than infer them from earlier one-shot processing tests.

## Remaining M3 work after runner acceptance

- video processing;
- perceptual duplicate detection;
- regeneration;
- progress/status UI.

Deferred cleanup/operational items:

- ingestion-source and processed-staging orphan reconciliation/janitor;
- deployment UID/GID/media-storage permissions;
- stronger `openat2`/`O_NOFOLLOW`-style hardening only if the local media tree threat model changes;
- explicit video CPU/thread/interference benchmark;
- v1-v2 apples-to-apples benchmark once equivalent end-to-end behavior exists.

## Local-agent rule

Use local/server agents for browser/DevTools, SSH, real PostgreSQL, target-server benchmarks, temporary deployments, or unavailable toolchains.

Default is read-only/execution-only. They must not modify source, SQL, docs, config, commits, branches, deployments, or persistent state without explicit approval for that specific write. Target/server evidence must return as one ZIP containing exact SHA/worktree state, commands, stdout/stderr, versions, raw measurements/plans, errors, cleanup proof, and concise findings.

## Single best next task

Validate the exact `astra/m3-worker-runner` feature HEAD with Rust/Cargo 1.99.x and disposable PostgreSQL. If and only if correctness passes with database tests actually executing and Cargo.lock unchanged, run the target-host continuous-runner performance gate against `/api/v2/posts/49999/around?radius=30`. Do not integrate into `v2` or start video until that evidence is reviewed.
