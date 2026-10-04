# Ginbar v2 state / handoff

Last updated: 2026-10-04
Phase: **M3 media pipeline — production still-image runner accepted and integrated; video processing next**
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
- M3 long-running production worker runner with polling, lease renewal, cancellation and graceful shutdown: **accepted and integrated**.
- Self-hosted v2 CI: **integrated**.
- M3 is **not complete**.

The runner gate is closed. Video processing may now begin, but video must receive its own codec/thread/resource/API-interference measurements before integration.

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
- publication remains fenced by owner/generation, source identity, post eligibility and lease validity;
- at-least-once work requires deterministic/idempotent side effects;
- still-image concurrency = **one media job at a time**;
- AVIF encoder threads = **one**;
- production idle poll = **500 ms**;
- production lease baseline = **30 s**, heartbeat at roughly one third of lease;
- PostgreSQL worker connect/statement budget = `min(lease / 10, 3s)`, 1 ms floor;
- any output-affecting media change requires a processing-version change rather than silent key reuse.

Do not add Redis wakeups, cgroups, CPU pinning, quotas or worker load admission from current evidence.

## Integrated still-image contract

Processing version 1 supports:

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
- deterministic canonical `.avif` and `<stem>.thumb.avif`;
- same-directory staged write + fsync + no-overwrite hard-link publication;
- exact size/SHA verification for idempotent reuse;
- conflicting existing destination bytes are terminal and never overwritten.

The accepted application lockfile is committed at `src/worker/v2/Cargo.lock`; use `--locked` for worker validation.

## Production runner contract

The integrated runner provides:

- serial claim/process/repeat behavior with at most one active media job;
- bounded idle polling rather than busy spin;
- independent PostgreSQL lease heartbeat while processing;
- owner/generation/expiry fencing on authoritative lifecycle mutations;
- cancellation on lost ownership and repeated renewal failure;
- bounded DB reconnect/renewal retry backoff;
- graceful SIGINT/SIGTERM behavior with owned-job requeue where possible;
- crash/restart lease reclaim onto the next generation;
- cancellation checks during source verification and before authoritative publication;
- bounded PostgreSQL connect/query operations derived from the lease.

AVIF encoding and durable filesystem publication are synchronous in processing v1 and cannot be interrupted mid-call. Cancellation is rechecked before the fenced database commit; stale work cannot release a post or complete a job, and deterministic no-overwrite files remain safe for retry.

## Runner correctness and integration evidence

Exact worker revision accepted by the target gate and fast-forwarded into `v2`:

- `534f9c3add3b4bec4cab405544f7741fa11d1b30`.

CI:

- feature-branch run `37163175288`: **success** on the exact target-gated revision;
- post-integration `v2` run `37167528287`: **success** on the same exact revision.

The worker CI gate covers Rust 1.99 formatting/checking, PostgreSQL-backed tests, Clippy with `-D warnings`, lockfile stability and clean checkout. Runner tests include repeated work without overlap, idle waiting, bounded retry, repeated lease renewal, generation loss, renewal DB failures, crash/restart reclaim, shutdown/requeue and lease-derived PostgreSQL timeout configuration.

## Accepted target-host runner gate

Evidence bundle:

- `m3-runner-20261004T001829Z.zip`;
- SHA-256 `f709ebc3c097e1d4fb88903cbda1dcdb6c90babda4df2ce1058226a66bdd57a3`.

The returned ZIP hash was independently verified before acceptance. Raw HTTP JSON was recomputed independently and matches the supplied comparison table.

Key results:

- idle runner: median CPU **0%**, peak **0.91%**, RSS **3,712 KiB**; clean SIGTERM in **30 ms**;
- forced renewal: 8192x8192 fixture completed in **2.322 s** under a 1 s lease with seven observed expiry values while attempt/generation stayed 1;
- continuous drain: **2,000/2,000 succeeded**, zero failures/stale running jobs, max running **1**, **1.505 jobs/s**;
- continuous worker CPU during active jobs: median **93.69%**, peak **99.01%**;
- continuous worker RSS: median **75,944 KiB**, peak **130,332 KiB**;
- shutdown during long encode safely requeued without publication/release; restart completed at attempt/generation 2;
- API coexistence: **80,000/80,000 measured requests succeeded**, zero errors.

Same-run median baseline -> loaded HTTP results:

| concurrency | p95 | p99 | throughput |
| ---: | ---: | ---: | ---: |
| 1 | 2.183 -> 2.283 ms (**+4.57%**) | 2.388 -> 2.548 ms (**+6.70%**) | 563.74 -> 533.11 req/s (**-5.43%**) |
| 2 | 2.360 -> 2.435 ms (**+3.20%**) | 2.551 -> 2.696 ms (**+5.68%**) | 991.21 -> 960.90 req/s (**-3.06%**) |
| 4 | 2.625 -> 2.830 ms (**+7.81%**) | 2.894 -> 3.183 ms (**+9.98%**) | 1,753.34 -> 1,688.46 req/s (**-3.70%**) |
| 8 | 3.947 -> 4.692 ms (**+18.88%**) | 4.833 -> 5.537 ms (**+14.57%**) | 2,667.60 -> 2,406.90 req/s (**-9.77%**) |

At c8 the loaded median max was **7.967 ms** and all requests still succeeded. The measurable peak-capacity cost is accepted; absolute latency remains within the current budget.

Cleanup evidence showed the disposable PostgreSQL container/volume and run tree removed, no benchmark processes remaining, Wallium/unrelated services still running, and the canonical checkout clean.

Stable details are consolidated in [`PERFORMANCE.md`](PERFORMANCE.md).

## Decisions from the runner gate

- keep one media job at a time;
- keep one AVIF encoder thread;
- keep the 500 ms PostgreSQL idle poll;
- keep the 30 s production lease/independent heartbeat model;
- keep API PostgreSQL pool cap 8;
- no Redis queue/wakeup dependency;
- no cgroup/core-pinning/quota/load-admission mechanism;
- treat CPU interference as a measured capacity trade-off rather than adding complexity without evidence.

## Remaining M3 work

Immediate next work:

- video processing architecture and first bounded implementation slice;
- explicit video codec/thread/resource/API-interference benchmark before acceptance.

After video:

- perceptual duplicate detection;
- regeneration;
- progress/status UI.

Deferred cleanup/operational items:

- ingestion-source and processed-staging orphan reconciliation/janitor;
- deployment UID/GID/media-storage permissions;
- stronger `openat2`/`O_NOFOLLOW`-style hardening only if the local media tree threat model changes;
- v1-v2 apples-to-apples benchmark once equivalent end-to-end behavior exists.

## Local-agent rule

Use local/server agents for browser/DevTools, SSH, real PostgreSQL, target-server benchmarks, temporary deployments or unavailable toolchains.

Before any local-agent handoff, require a green applicable CI pipeline for the exact revision being handed off. Do not use the local agent to discover routine correctness failures that CI can catch.

Default is read-only/execution-only. They must not modify source, SQL, docs, config, commits, branches, deployments or persistent state without explicit approval for that specific write.

Return one evidence ZIP with exact SHA/worktree state, commands, stdout/stderr, environment versions, raw benchmark/query-plan data, errors, cleanup proof and concise findings.

## Single best next task

Start the smallest coherent **video-processing boundary** from current `v2`: inspect the existing source-sniffing/publication contracts and legacy behavior only as reference, choose a proven bounded video toolchain, define deterministic output/versioning and cancellation/resource limits, implement correctness tests first, then run the applicable CI before any target-host codec/interference handoff.
