# Ginbar v2 state / handoff

Last updated: 2026-10-04
Phase: **M3 media pipeline — still-image processing integrated; production worker loop next**
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
- M3 is **not complete**.

Next milestone work is the long-running production worker loop with lease renewal/cancellation/lost-ownership handling. Do not start video before that runner boundary is validated.

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

The accepted application lockfile is committed at `src/worker/v2/Cargo.lock`. Its measured graph uses the exact validated lock bytes from the final image gate; use `--locked` for worker validation.

## Current performance decisions

Stable measurements are consolidated in [`PERFORMANCE.md`](PERFORMANCE.md). Immediate constraints that matter for the next slice:

- frontend selection sync p95 stayed below ~0.5 ms through the tested 10,000-post M1 matrix;
- M2 around-post p95 ranged from 2.277 ms at c1 to 13.712 ms at c32 with zero errors in the final gate;
- one large image job has little tail-latency impact at low API concurrency but reduces peak HTTP capacity near CPU saturation;
- accepted image configuration remains one job / one AVIF encoder thread;
- do not add scheduler/load-admission complexity until the real continuous runner is measured;
- there is **no apples-to-apples global v1-v2 benchmark yet**, so do not claim an overall rewrite speedup.

## Repository/documentation consolidation completed

The rewrite tree/docs were cleaned up without changing v2 product semantics:

- root README now describes v2 rather than the legacy Go/Fiber/React stack;
- `docs/v2/README.md` is the documentation index;
- `PERFORMANCE.md` owns stable benchmark history;
- `PLAN.md` was refreshed from stale pre-implementation status to current M1/M2/M3 progress;
- `STATE.md` was reduced to operational handoff information;
- completed M1/M2 execution runbooks moved under `docs/v2/archive/`;
- tracked legacy build artifact `src/backend/wallium-backend` (~22 MiB) removed and ignored;
- tracked `src/worker/desktop.ini` removed; common OS metadata is ignored.

Legacy source itself was deliberately retained as reference material while the rewrite remains incomplete.

## CI / handoff status

- `astra/v2-self-hosted-ci` contains the isolated self-hosted CI gate targeting `ginbar-ci-vm` / `amp-ci-vm` and the shared host benchmark-lock relay.
- Full `scope=all` validation passed on GitHub Actions run `37160895027` at commit `ad46a3ed279b9a80f867e76512082dec56ef04a4`.
- The CI feature branch is not yet integrated into `v2`.
- Project instructions now require the applicable CI pipeline for the exact revision being handed to the local agent to be green first. Routine formatting, compiler, lint, test, and other correctness failures must be fixed before handoff; infrastructure-blocked CI is a handoff blocker unless the user explicitly authorizes bypass.

## Remaining M3 work

- production worker polling/wakeup loop;
- lease renewal during source verification/codec/filesystem work;
- cancellation and lost-ownership handling;
- bounded retry/backoff;
- graceful shutdown;
- crash/restart/lease-loss tests;
- continuous-runner API interference benchmark;
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

Integrate the validated **self-hosted v2 CI gate** into `v2`, verify the `v2` pipeline is green, then resume the production worker loop + lease-renewal/cancellation slice.

Do not begin video processing until the production runner boundary passes its correctness and performance gate.
