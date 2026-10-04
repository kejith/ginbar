# Ginbar v2 state / handoff

Last updated: 2026-10-04
Phase: **M3 media pipeline — production still-image runner accepted; bounded video probe boundary integrated; video publication next**
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
- M3 bounded video metadata/probe compatibility boundary: **integrated**.
- Self-hosted v2 CI: **integrated**.
- M3 is **not complete**.

Current integration tip after the video-probe slice: `cf4f40630fec23844a3a2898511840f1a17dc71f`.

The video probe slice does **not** yet publish or release video posts. Production job execution still rejects video after verified-source preparation. The next slice is canonical compatible-video publication + thumbnail generation + runner dispatch.

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
- media worker concurrency remains **one job at a time**;
- still-image AVIF encoder threads remain **one**;
- production idle poll = **500 ms**;
- production lease baseline = **30 s**, heartbeat at roughly one third of lease;
- PostgreSQL worker connect/statement budget = `min(lease / 10, 3s)`, 1 ms floor;
- any output-affecting media change must use explicit deterministic processing-version identity rather than silently reusing incompatible output.

Do not add Redis wakeups, cgroups, CPU pinning, quotas or worker load admission from current evidence.

## Accepted still-image contract

Processing version 1 supports JPEG, non-animated PNG and non-animated WebP.

Intentional terminal/out-of-scope still-image inputs remain animated PNG/WebP, GIF and AVIF/HEIF input.

Accepted still-image limits/output:

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

## Production runner contract and accepted target gate

The integrated runner provides serial claim/process/repeat behavior, bounded idle polling, an independent PostgreSQL lease heartbeat, lost-ownership cancellation, bounded retry/backoff, graceful SIGINT/SIGTERM handling, crash/restart reclaim and bounded PostgreSQL operations.

AVIF encoding and durable filesystem publication are synchronous in processing version 1 and cannot be interrupted mid-call. Cancellation is rechecked before the fenced database commit; stale work cannot release a post or complete a job.

Exact target-gated runner revision: `534f9c3add3b4bec4cab405544f7741fa11d1b30`.

Evidence bundle:

- `m3-runner-20261004T001829Z.zip`;
- SHA-256 `f709ebc3c097e1d4fb88903cbda1dcdb6c90babda4df2ce1058226a66bdd57a3`.

Key accepted evidence:

- idle runner: median CPU **0%**, peak **0.91%**, RSS **3,712 KiB**, clean SIGTERM in **30 ms**;
- forced 1 s lease renewal: long 8192x8192 job retained attempt/generation 1 across seven observed expiry values and succeeded;
- continuous drain: **2,000/2,000 succeeded**, zero failed/stale jobs, max running **1**, **1.505 jobs/s**;
- active worker CPU median **93.69%**, peak **99.01%**; RSS median **75,944 KiB**, peak **130,332 KiB**;
- shutdown during long encode requeued without authoritative publication/release; restart completed at attempt/generation 2;
- API coexistence: **80,000/80,000 measured requests succeeded**, zero errors;
- at c8, loaded p95 **4.692 ms** vs **3.947 ms** baseline (**+18.88%**) and throughput **2,406.90** vs **2,667.60 req/s** (**-9.77%**).

Decision: keep the simple PostgreSQL polling/heartbeat architecture, one media job at a time and one still-image encoder thread. No scheduler/wakeup/admission mechanism is justified now. Stable details live in [`PERFORMANCE.md`](PERFORMANCE.md).

## Integrated video probe boundary

`src/worker/v2/src/video.rs` establishes the first bounded video-processing contract without enabling publication yet.

Architecture decisions:

- use the proven external `ffprobe` CLI rather than adding codec/container parsing bindings to Rust;
- no new Rust dependency or lockfile change for this boundary;
- subprocess execution uses direct argv, never a shell;
- stdin is closed, stdout/stderr go to bounded capture files, and the child is polled for cancellation;
- probe timeout: **10 s**;
- cancellation/timeout poll interval: **20 ms**;
- stdout/stderr cap: **64 KiB each**;
- spawn/I/O failures are retryable; deterministic invalid/unsupported metadata and probe timeout are terminal;
- source bytes remain authoritative from the existing verified-source SHA/size/sniff boundary.

Video processing version 1 compatibility policy currently defined by the probe:

- accepted container family for passthrough: **MP4 only**;
- exactly one video stream;
- video codec: **H.264**;
- pixel format: **8-bit 4:2:0** (`yuv420p` or `yuvj420p`);
- audio: **AAC or no audio**;
- positive finite duration;
- positive dimensions, max dimension **16,384 px**, max frame pixels **80,000,000**;
- EBML is recognized but deliberately rejected rather than silently relabeled as WebM;
- HEVC, 10-bit/other pixel formats, non-AAC MP4 audio and other incompatible inputs explicitly require a future transcode path rather than implicit conversion.

This policy is intentionally conservative for a first zero-transcode path. It can be expanded only with browser-compatibility evidence and target-host measurements.

CI evidence for the probe boundary:

- feature branch exact tip `cf4f40630fec23844a3a2898511840f1a17dc71f`: run `37168411203`, **success**;
- post-integration `v2` at the same exact SHA: run `37168482641`, **success**.

CI covers Rust 1.99 format/check, all worker/PostgreSQL tests, Clippy with `-D warnings`, lockfile stability and clean checkout. Probe tests cover compatible MP4, no-audio MP4, codec/pixel-format/audio rejection, multiple-video-stream rejection, dimension/pixel limits, duration validation and explicit EBML rejection.

## Remaining M3 work

Immediate next slice:

1. add deterministic canonical MP4 passthrough publication without buffering the whole video in memory;
2. generate a bounded 256x256 video thumbnail using an explicitly one-thread external FFmpeg decode path and the existing AVIF thumbnail encoder contract;
3. dispatch verified image/video sources through one production executor while retaining one active media job;
4. ensure cancellation kills bounded external video subprocesses and rechecks ownership before fenced publication;
5. add integration tests for idempotent retry/collision/publication/restart semantics;
6. run exact-revision CI;
7. only then hand the exact green revision to the target host for real video codec/probe/thumbnail/resource/API-interference measurements.

After the first compatible-video path:

- decide from real input/product evidence whether a transcode fallback is necessary and which codec/container it should target;
- add exact WebM/EBML distinction before accepting WebM passthrough;
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

Implement the **zero-transcode compatible-MP4 processor** from current `v2`: deterministic canonical publication plus bounded one-frame thumbnail extraction, then wire it into the accepted runner and validate correctness before any target-host video benchmark.
