# Ginbar v2 state / handoff

Last updated: 2026-10-04
Phase: **M3 media pipeline — compatible-MP4 zero-transcode processor implemented; target-host video gate pending**
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
- M3 compatible-MP4 zero-transcode processor: **implemented on a green feature revision; target-host performance gate required before integration**.
- Self-hosted v2 CI: **integrated**; the feature candidate also hardens the workflow to use GitHub Actions `runner.temp` rather than assuming `/tmp` exists on the self-hosted VM.
- M3 is **not complete**.

Current `v2` tip: `2b15aeef5662844bfc39fb62d05b5c53d49295a4`.

Current clean video benchmark candidate on `astra/m3-video-passthrough`:

- feature commit: `a6b5ab8624e1a7c81d7ba6bdff598c3933fc38c3` (`feat(v2): publish compatible MP4 video`);
- CI-environment fix: `e8ad6e1d5f2129a8cd869585068ee6489d4be4a8` (`ci(v2): use runner temp directory`);
- exact candidate to benchmark: **`e8ad6e1d5f2129a8cd869585068ee6489d4be4a8`**;
- exact full-CI run: **`37206303449`**, success.

Do not integrate that candidate into `v2` until the target-host video performance/coexistence gate is accepted.

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
- still-image/video-thumbnail AVIF encoder threads remain **one**;
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

AVIF encoding and durable filesystem publication are synchronous in processing version 1. Cancellation is rechecked before the fenced database commit; stale work cannot release a post or complete a job.

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

Decision: keep the simple PostgreSQL polling/heartbeat architecture, one media job at a time and one AVIF encoder thread. No scheduler/wakeup/admission mechanism is justified now. Stable details live in [`PERFORMANCE.md`](PERFORMANCE.md).

## Compatible-MP4 zero-transcode candidate

The exact green candidate `e8ad6e1d5f2129a8cd869585068ee6489d4be4a8` extends the integrated video-probe boundary into actual processing/publication without adding video transcoding.

### Processing contract

Video processing version 1 accepts only a deliberately narrow browser-compatible MP4 path:

- container must be reported as MP4 by `ffprobe`;
- exactly one video stream;
- video codec **H.264**;
- pixel format **8-bit 4:2:0** (`yuv420p` or `yuvj420p`);
- audio **AAC or no audio**;
- positive finite duration;
- coded/display dimensions positive, max dimension **16,384 px**, max frame pixels **80,000,000**;
- sample aspect ratio must be **1:1** for this version;
- orthogonal display rotation is accepted and normalized; 90/270-degree rotation swaps reported display width/height;
- conflicting/non-orthogonal rotation metadata is rejected;
- EBML/WebM, HEVC, 10-bit/other pixel formats, non-AAC audio and non-square-pixel video remain terminal/out of scope and require a separately defined future path.

### Canonical publication

- compatible MP4 bytes are **not transcoded**;
- canonical key uses the existing post/source-digest/processing-version identity and `.mp4` output format;
- the source is streamed into same-directory staging rather than buffered in RAM;
- the stream is re-hashed and re-sized while staging, so source mutation after initial verification is rejected;
- staging is fsynced, then hard-linked into the final path without overwrite, final directory fsynced, and staging cleaned;
- an identical existing canonical object is reused after streamed size/SHA verification;
- conflicting existing bytes are terminal and never overwritten;
- cancellation is checked during streamed copy and existing-output verification.

### Thumbnail path

- one bounded external `ffmpeg` process extracts exactly one PNG frame from the **durable canonical MP4**, not the mutable ingestion path;
- stdin is closed;
- process timeout is **20 s**;
- command polling/cancellation interval remains **20 ms**;
- decoder/filter/encoder thread limits are explicitly one where FFmpeg exposes them;
- FFmpeg autorotation remains enabled so display-matrix rotation is applied before scaling;
- extracted frame is bounded to at most **512x512** and stdout is capped at **4 MiB**;
- stdout/stderr are drained concurrently into bounded memory, avoiding pipe deadlock and unbounded capture;
- cancellation/timeout kills and reaps the child;
- the frame is decoded in Rust and passed through the shared still-image AVIF thumbnail encoder contract, producing deterministic `<stem>.thumb.avif`;
- the auxiliary thumbnail is durable before generation-fenced DB publication can mark media ready/release the post.

### Runner wiring/fencing

- new `MediaJobExecutor` dispatches verified image/video sources while reusing the accepted `WorkerRunner` loop unchanged;
- the production `run` command uses `MediaJobExecutor` and retains one active media job;
- `process-once` can process the compatible video path as well as images;
- cancellation/lost ownership after filesystem work returns `Cancelled`/`OwnershipLost` rather than authoritatively publishing stale results;
- `publish_processed` remains the sole authoritative generation-fenced DB publication path.

### Correctness evidence

Exact clean candidate: `e8ad6e1d5f2129a8cd869585068ee6489d4be4a8`.

Exact full v2 CI run: `37206303449`, **success**.

The gate covered:

- Rust 1.99 formatting/check;
- all Rust unit tests;
- PostgreSQL-backed worker integration tests;
- Clippy with `-D warnings`;
- Cargo lockfile stability;
- Go 1.25 backend tests;
- frontend tests/typecheck/build;
- clean tracked checkout.

Worker results on that candidate included:

- **39** library unit tests passed;
- **3** worker-binary unit tests passed;
- **2** image-pipeline integration tests passed;
- **5** job-state tests passed;
- **5** processing-contract tests passed;
- **6** runner-loop tests passed;
- **4** new video-pipeline integration tests passed;
- zero failures/skips in the required PostgreSQL-backed worker gate.

Focused video/output coverage includes compatible/no-audio MP4 metadata, rotation/display dimensions, unsupported codec/audio/pixel format/SAR/container, multiple/no video streams, invalid dimensions/duration, subprocess timeout/cancellation/reaping, deterministic canonical identity, streamed idempotent reuse, mutation/collision rejection, exact canonical byte size/SHA, successful video runner publication/release, terminal failure classification, and lease loss after durable canonical publication without stale media/post release.

No target-host performance claim has been accepted for this video path yet.

## CI environment note

The self-hosted CI VM currently does not provide `/tmp`. The exact clean video candidate therefore also changes `.github/workflows/v2-ci.yml` to set:

- `TMPDIR: ${{ runner.temp }}`;
- `GINBAR_HOST_GATE_LOCK: ${{ runner.temp }}/ginbar-v2-host-gate.lock`.

This preserves the existing correctness script and read-only workflow permissions while removing an invalid host `/tmp` assumption. Full CI passes with that change.

## Remaining M3 work

Immediate next task is the **target-host compatible-video performance/coexistence gate** on exact revision `e8ad6e1d5f2129a8cd869585068ee6489d4be4a8`.

It must measure at minimum:

1. zero-transcode compatible-MP4 publication throughput and exact canonical-byte identity;
2. thumbnail extraction cost;
3. worker CPU/RSS and FFmpeg/FFprobe CPU/thread behavior;
4. lease renewal during a sufficiently long video operation;
5. cancellation/shutdown/restart fencing behavior;
6. API baseline versus continuous active video processing at HTTP c1/c2/c4/c8, including p50/p95/p99/max, throughput and zero-error verification;
7. coexistence with the target host's normal shared workload left running.

Do not integrate the video candidate or add a transcode path before this gate is reviewed.

After an accepted zero-transcode gate:

- record accepted target measurements in [`PERFORMANCE.md`](PERFORMANCE.md);
- fast-forward the accepted commits into `v2`, rerun exact post-integration CI, and update this state file;
- only then decide from real product/input evidence whether a transcode fallback is necessary and which exact codec/container contract it should target;
- add exact WebM/EBML distinction before accepting WebM passthrough;
- later add perceptual duplicate detection, regeneration and progress/status UI.

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

Run the **read-only/execution-only target-host video gate** against exact green revision `e8ad6e1d5f2129a8cd869585068ee6489d4be4a8`, return one retained evidence ZIP, then decide here whether the zero-transcode MP4 path is acceptable for integration.