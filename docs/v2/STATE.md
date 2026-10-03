# Ginbar v2 state / handoff

Last updated: 2026-10-04
Phase: **M3 media pipeline — production runner correctness complete; target-host performance gate next**
Integration branch: `v2`
Legacy branch: `master` (read-only for rewrite work)

Read this file first. Use [`PLAN.md`](PLAN.md) for stable architecture/milestone rules and [`PERFORMANCE.md`](PERFORMANCE.md) for accepted benchmark history. Do not use chat history as project memory.

## Current rewrite status

- M1 board performance prototype: **complete and integrated**.
- M2 fresh PostgreSQL schema + core Go API: **complete and integrated**.
- M3 durable PostgreSQL media-job ownership/retry/recovery: **integrated**.
- M3 upload/URL ingestion + durable enqueue: **integrated**.
- M3 source verification + generation-fenced DB publication: **integrated**.
- M3 still-image JPEG/PNG/WebP -> deterministic AVIF canonical + thumbnail pipeline: **integrated**.
- Self-hosted v2 CI: **integrated and green on `v2`**.
- M3 long-running production worker runner: **correctness-gated on feature branch; target-host performance gate pending**.
- M3 is **not complete**.

Do not integrate the runner into `v2` or start video until the real continuous-runner target-host gate is accepted.

## Source boundaries

- `src/frontend/` — accepted SolidJS + TypeScript + Vite board/frontend prototype.
- `src/backend/v2/` — clean v2 Go API/schema/search/bench code.
- `src/worker/v2/` — clean v2 Rust worker and media pipeline.
- adjacent non-v2 source remains legacy/reference-only.

`master` must not be modified. Always verify current refs before writes and branch new implementation work from current `v2`.

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

Processing version 1 currently supports:

- JPEG;
- non-animated PNG;
- non-animated WebP.

Current intentional terminal/out-of-scope inputs:

- animated PNG/WebP;
- GIF;
- AVIF/HEIF input;
- video.

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

The accepted application lockfile is committed at `src/worker/v2/Cargo.lock`. Use `--locked` for worker validation.

## Production runner candidate

Branch: `astra/m3-worker-runner-ci`.

The candidate now provides:

- serial one-job-at-a-time claim/process/repeat behavior;
- bounded idle polling rather than busy spinning;
- independent PostgreSQL lease heartbeat while processing;
- renewal at roughly one third of the configured lease;
- generation/owner/expiry fencing on all authoritative lifecycle mutations;
- cancellation on lost ownership and repeated renewal failure;
- bounded DB reconnect and renewal retry backoff;
- graceful SIGINT/SIGTERM behavior with owned-job requeue where possible;
- crash/restart lease reclaim semantics;
- cancellation checks around source verification, processing and before authoritative publication;
- lease-aware PostgreSQL connection/statement bounds: `min(lease / 10, 3s)`, 1 ms floor;
- preserved PostgreSQL connection `options`, with worker `statement_timeout` appended.

AVIF encoding and durable filesystem publication are synchronous in processing v1 and cannot be interrupted mid-call. The worker rechecks cancellation before the fenced database commit, so stale work cannot release a post or complete a job. Deterministic no-overwrite files produced before cancellation remain safe for retry.

The PostgreSQL socket connect timeout applies per address attempt. The current production architecture is local PostgreSQL; multi-host failover timing is not part of this runner contract.

## Runner correctness evidence

Current documented candidate code/docs revision before this state-only update:

- `3e20101480a325f6de43b7f3e1ff81d203701622`
- GitHub Actions run `37163020555`: **success** (`scope=worker`).
- immediately preceding code revision `96b77a3d8339b0c099d2e4f70388ad0e65917f9d`: **success**, run `37162981139`.

The worker gate covers Rust 1.99 formatting/checking, PostgreSQL-backed tests, Clippy with `-D warnings`, lockfile stability, and clean checkout. Relevant runner coverage includes:

- repeated successful work without overlapping active jobs;
- idle polling without busy spin;
- bounded retry backoff;
- long-running job lease renewal;
- lost-generation cancellation without stale completion;
- repeated renewal DB failure classification/requeue;
- crash/restart reclaim onto the next generation;
- shutdown during active work and owned-job requeue;
- lease-derived PostgreSQL timeout configuration and existing-option preservation.

During CI hardening, a 120 ms lease / 350 ms renewal test proved scheduler-sensitive on the self-hosted runner. The test was changed to a 1 s lease with 2.5 s active work: it still requires repeated renewals but gives realistic CI scheduling/DB margin. Production default lease remains 30 s.

## Current performance decisions

Stable measurements are consolidated in [`PERFORMANCE.md`](PERFORMANCE.md). Immediate constraints for the runner gate:

- frontend selection sync p95 stayed below ~0.5 ms through the tested 10,000-post M1 matrix;
- M2 around-post p95 ranged from 2.277 ms at c1 to 13.712 ms at c32 with zero errors in the final gate;
- one large image job has little tail-latency impact at low API concurrency but reduces peak HTTP capacity near CPU saturation;
- accepted image configuration remains one job / one AVIF encoder thread;
- prior synthetic one-image coexistence evidence is not a substitute for a real continuous runner measurement;
- do not add scheduler/load-admission complexity until the continuous runner is measured;
- there is **no apples-to-apples global v1-v2 benchmark yet**, so do not claim an overall rewrite speedup.

## CI / integration status

The consolidated self-hosted CI gate is integrated into `v2` at `56bc4845114fd48af5e492c3412fba4296c15baf`.

- `v2` run `37162024614` at that exact revision: **success**.
- CI runs on `ginbar-ci-vm` / `amp-ci-vm` and scopes frontend/backend/worker work automatically.
- Exact-revision CI must be green before any local/server agent handoff.
- The runner feature branch is intentionally **not yet integrated into `v2`** because the target-host performance/coexistence gate remains open.

## Remaining M3 work

Immediate runner gate:

- target-host idle runner CPU/RSS and PostgreSQL query activity;
- continuous still-image job throughput/resource use;
- forced-renewal behavior using a long fixture and shorter test lease;
- API p50/p95/p99/max latency and throughput baseline vs continuous runner;
- cleanup/restoration evidence on the shared host;
- accept/reject the one-job/one-encoder-thread runner architecture from measured evidence.

After the runner gate is accepted and integrated:

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

Before any local-agent handoff, require a green applicable CI pipeline for the exact revision being handed off. Do not use the local agent to discover routine correctness failures that CI can catch.

Default is read-only/execution-only. They must not modify source, SQL, docs, config, commits, branches, deployments, or persistent state without explicit approval for that specific write.

Return one evidence ZIP with exact SHA/worktree state, commands, stdout/stderr, environment versions, raw benchmark/query-plan data, errors, cleanup proof, and concise findings.

## Single best next task

Run the exact CI-green `astra/m3-worker-runner-ci` revision through the target-host continuous-runner gate using disposable PostgreSQL/media state while leaving the shared workload in its normal state. Return the evidence ZIP here for analysis. If the evidence is acceptable, integrate the runner into `v2`, update `PERFORMANCE.md`/`STATE.md`, and only then begin video processing.
