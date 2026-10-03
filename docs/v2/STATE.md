# Ginbar v2 State / Handoff

Last updated: 2026-10-03
Phase: M3 image-processing slice — CORRECTNESS PASSED; API-INTERFERENCE PERFORMANCE GATE BLOCKS INTEGRATION
Integration branch: `v2`
Active feature branch: `astra/m3-image-processing`
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the resume point. Read it before `PLAN.md`. Do not rely on chat history as project memory.

## Branch status

- `master` remains untouched by rewrite work at `181fa44d79c7b4a1984c1a35795762dd503b3f77`.
- `v2` remains unchanged by the image-processing candidate at `e601c78486d219f97f98e55349209849f79c353f`.
- M1 board benchmark is complete.
- M2 fresh schema + core Go API is complete and integrated.
- M3 durable PostgreSQL media-job ownership/recovery boundary is complete and integrated.
- M3 upload/URL-ingestion + durable-enqueue boundary is complete and integrated.
- M3 Rust worker source-consumption/fenced-publication contract is complete, validated, and integrated.
- Image-processing source candidate tested by the final gate: `e53605838075509800efeaa644319d29e18587f2` on `astra/m3-image-processing`, merge base exactly `v2` `e601c78486d219f97f98e55349209849f79c353f`.
- The final gate passed correctness, DB-backed tests, release builds, media processing, crash/idempotency/collision behavior, and deterministic DB/filesystem publication.
- Integration is still blocked because one concurrent large-image worker materially degraded API latency/throughput on the target host.
- Do not merge image processing into `v2` until worker resource isolation/priority is measured and the API-interference gate is acceptable.
- Worker v2 lives under `src/worker/v2`; legacy `src/worker` remains reference-only unless explicitly reviewed for reuse.
- `.local-agent-results/` remains ignored for evidence ZIPs.

## Retained architecture / decisions

Keep unless new evidence contradicts them:

- Go 1.25 + pgx/v5 + standard `net/http` for API/workflows;
- PostgreSQL authoritative for app/workflow/durable job state;
- Rust worker for media processing;
- local NVMe media storage;
- no Redis without measured need;
- immutable numeric relational IDs;
- primary feed uses post-ID cursor pagination, never OFFSET;
- durable media jobs use PostgreSQL and generation-fenced ownership;
- at-least-once processing requires deterministic/idempotent durable side effects;
- initial media-worker concurrency is one job at a time;
- source verification and codec/filesystem work stay outside DB transactions;
- final media publication is fenced by job ownership/generation, source identity, post eligibility, and post-lock real-time lease expiry;
- worker CPU/resource policy must protect API latency under concurrent processing; one-job concurrency alone is not sufficient evidence of isolation.

## Integrated ingestion / worker baselines

Accepted ingestion baselines:

- 8 MiB durable source staging: **45.1468 ms/op**, **186.06 MB/s**;
- PostgreSQL ingestion CTE c1: **0.348 ms**, **2,870.87 TPS**;
- PostgreSQL ingestion CTE c4: **0.457 ms**, **8,761.36 TPS**.

Accepted source verification baseline, target host, release mode, warm page cache, 128 KiB buffer:

- 8 MiB: **35.104 ms**, **227.91 MiB/s**;
- 64 MiB: **298.757 ms**, **214.92 MiB/s**;
- 256 MiB: **1,122.598 ms**, **228.05 MiB/s**.

Accepted fenced publication SQL baseline, PostgreSQL 17.11:

- 1,000/1,000 successful publications;
- **1.539 ms** average statement latency;
- **644.22 TPS**;
- bounded indexed plan, no unbounded sequential scan;
- one-row `EXPLAIN (ANALYZE, BUFFERS)` execution **0.949 ms**.

Prior validated source/publication evidence: `m3-worker-processing-contract-final-20261003T013900Z.zip`, SHA-256 `14DA7E1C4B8FB5495EFA6D2C1C920FD01CCF724F7441EFE5A928887216238ACB`, tested SHA `37ba916fad6e265f8e8ffde7b742533f8c55425a`.

## M3 image-processing candidate

### Durable no-overwrite output publication

`src/worker/v2/src/output.rs`:

- output keys are relative below `media/` and reject invalid path components;
- output root/components reject symlinks/non-directories;
- same-directory staging uses `create_new`;
- staged bytes are fully written and file-fsynced;
- final publication uses same-filesystem `hard_link`, so an existing final cannot be overwritten;
- final directory is fsynced after publication;
- staging cleanup is followed by another directory fsync;
- existing finals are accepted only after exact byte-size + SHA-256 verification;
- differing deterministic-target bytes are terminal collisions and never overwritten.

Crash tests cover failure after staging fsync and after final-directory fsync. Hidden staging orphans remain a later janitor concern, not a correctness ambiguity.

### Bounded still-image processor

`src/worker/v2/src/image.rs` supports:

- JPEG;
- non-animated PNG;
- non-animated WebP.

Animated PNG/WebP, GIF, AVIF/HEIF input, and video are terminal in processing version 1.

Candidate processing-v1 constants/behavior:

- full decode before any durable output publication;
- decoder orientation applied and dimensions revalidated;
- max input dimension 16,384 px;
- max decoded pixels 80,000,000;
- decoder allocation hint 384 MiB;
- canonical output fits within 1,280 px, no upscale;
- 256x256 center-crop thumbnail via view + resize, avoiding a full-size crop allocation;
- original full decode dropped after main-image downsize;
- both AVIFs encoded before either is published;
- `ravif`: speed 10, main quality 75, thumbnail quality 60, explicit `with_num_threads(Some(1))`;
- deterministic auxiliary `<stem>.thumb.avif`, canonical deterministic `.avif`;
- canonical AVIF SHA-256/size/dimensions feed the validated fenced DB publication.

Any output-affecting change after integration requires incrementing `PROCESSING_VERSION`.

`process-once` claims and processes at most one still-image job. It is a validation/execution probe, not the eventual polling runner; it does not yet renew leases during codec work.

## Image-processing validation history

### First target gate — FAIL

Evidence: `m3-image-processing-20261003T123812Z.zip`, SHA-256 `1153CB218BC4654B0E57252236EBC638C87A02A7A124C3A7B51D6AA6E5EB9322`, tested SHA `56ead29cc0f14fabf84469147faa65ecbe508a5f`.

It used Rust 1.94 instead of required 1.99 and exposed real formatting/API errors (`SubImage` resize and `rgb::FromSlice`). No tests/benchmarks ran. Corrected without output-semantic changes.

### Second corrective gate — PARTIAL

Evidence: `m3-image-processing-corrective-20261003T130603Z.zip`, SHA-256 `C7CBC73C08E6E2F7E44C3FD94B2F91B2FBE0C4AE96EB42C77E6E3E1B2A484652`, tested SHA `6e1eba20155202841adb3717f224b2266d82627e`, Rust/Cargo 1.99.0.

- rustfmt passed;
- normal tests compiled and returned 34 successes;
- one real Rust 1.99 Clippy lint remained (`chunks_exact_mut(4)` test helper);
- two requested Cargo commands were malformed by the harness (`--locked=false`, and a CR in the feature-tree edge argument).

The Clippy pattern was corrected in both the unit and DB image test helpers through `a718de413673e7650c188c659fc4627b26eb0d3c`.

### Final full target gate — CORRECTNESS PASS / PERFORMANCE BLOCKER

Evidence ZIP: `m3-image-processing-final-20261003T143503Z.zip`.
ZIP SHA-256: `2DC7980BC81342E770768DD4F460484F422B549E025ACA8D1EAAFABA135E9045`.
Exact tested candidate: `e53605838075509800efeaa644319d29e18587f2`.
Target: Ubuntu 24.04.3, Linux 6.8.0-88-generic, Intel i7-7700 (4 cores / 8 logical CPUs), ext4 RAID1 over NVMe, PostgreSQL 17.11.
Toolchain: rustc 1.99.0, cargo 1.99.0, rustfmt 1.10.0-stable, clippy 0.1.99, NASM 2.16.01.

Correctness/release gate passed:

- `cargo fmt --check`: pass;
- `cargo check`: pass;
- `cargo clippy --all-targets -- -D warnings`: pass;
- no-DB `cargo test`: 34 passed, 0 failed/ignored;
- normal dependency tree: pass;
- feature dependency tree: pass on clean LF-native rerun;
- PostgreSQL-enabled full suite: 29 passed, 0 failed/ignored;
- explicit `image_pipeline`: 2 passed;
- release worker build: pass;
- release `image_bench` build: pass.

DB-backed validation included predecessor job/publication tests plus image crash-after-files-before-DB retry/idempotency and deterministic collision preservation. Six disposable end-to-end worker jobs each succeeded exactly once, cleared ownership, released the post, created exactly one ready canonical media row, and produced canonical/thumbnail files whose hashes matched expected deterministic identities. Existing unrelated sentinel bytes remained unchanged.

Generated dependency lock SHA-256 was again exactly `4B355C9016EF56D71C78D4CFCC347BF0A6FD2F3DBA8E6F36465B6D3EDEDAA74A`. The final ZIP accidentally omitted the lockfile bytes despite reporting the hash; the preceding corrective evidence ZIP contains the exact lockfile bytes with that same hash. A committed `Cargo.lock` remains the intended production choice for this binary before final integration.

### Accepted codec/resource measurements from final gate

Five iterations each:

- Wikimedia `Example.jpg`, 172x178: verify median **0.105 ms**, process median **79.145 ms**, output 4,257 B;
- Wikimedia `Fronalpstock_big.jpg`, 10109x4542: verify median **71.988 ms**, process median **1,292.111 ms**, output 67,426 B at 1280x575;
- Wikimedia transparency PNG, 800x600: verify median **3.021 ms**, process median **259.259 ms**, output 24,299 B;
- Google WebP gallery `1.webp`, 550x368: verify median **0.553 ms**, process median **170.883 ms**, output 29,954 B.

Large deterministic valid probe:

- PNG 8192x8192 = 67,108,864 pixels, below 80 MP cap;
- source 919,484 B;
- process **1,923.785 ms** in standalone image benchmark;
- output 390 B at 1280x1280;
- wall 1.93 s, user 1.72 s, system 0.16 s;
- max RSS **369,844 KiB**.

The output is tiny because the deterministic synthetic image is highly compressible; this does not represent photographic output-size behavior.

### API interference result — BLOCKING

Existing v2 `httpbench.go`, endpoint `/api/v2/posts/49999/around?radius=30`, concurrency 8, 2,000 measured requests per round. Three baseline and three valid loaded rounds all returned 2,000 HTTP 200 responses with zero errors. Each primary loaded round had exactly one active image job before and after the HTTP interval.

Median-of-three comparison:

| Metric | Baseline | One image worker | Change |
| --- | ---: | ---: | ---: |
| p50 | 3.071 ms | 3.557 ms | **+15.81%** |
| p95 | 4.733 ms | 5.864 ms | **+23.89%** |
| p99 | 5.678 ms | 6.884 ms | **+21.24%** |
| max | 7.819 ms | 8.830 ms | **+12.93%** |
| throughput | 2,386.6 req/s | 2,106.6 req/s | **-11.73%** |

Loaded p95 rounds were 4.554, 6.760, and 5.864 ms, so run-to-run variance is nontrivial, but the median degradation is large enough to treat as material rather than accept it as noise.

The large worker jobs used about one CPU: `/usr/bin/time -v` reported **97–98% CPU** and ~369.8 MiB max RSS, with image times about 2.4–2.7 s during loaded rounds. Runtime thread observation showed two worker threads through decode/resize and up to 11 threads during the rav1e phase; almost all observed CPU ticks still accumulated on one execution thread, consistent with roughly one-core total CPU rather than accidental multi-core encode saturation.

Decision: **do not integrate this image-processing slice into `v2` yet.** Correctness is accepted; API interference is the remaining blocker.

## Remaining observations / deferred work

- Full source SHA-256 verification still costs a sequential read before codec consumption; do not redesign without profiling evidence.
- Standard-library canonicalization/symlink checks are not Linux `openat2`/`O_NOFOLLOW` race-proof against a malicious local filesystem writer; current media tree is service-controlled.
- Ingestion source orphans and processed staging orphans need eventual janitor/reconciliation work.
- The one-shot worker does not renew leases; the production polling runner needs measured renewal/cancellation behavior later in M3.
- Deployment must ensure API and worker share the media root with compatible UID/GID/permissions.
- The next performance work should prefer OS/cgroup scheduling/resource isolation over codec micro-optimization: measurements show the worker already uses roughly one CPU and explicit one-thread AVIF, while the problem is API contention on the shared target host.

## Local-agent evidence workflow

For target-host work, use isolated/disposable resources and return one ZIP with exact SHA/status, commands, raw stdout/stderr, environment/tool versions, measurements, invalid attempts, and cleanup/restoration evidence. Preserve raw remote evidence until reviewed.

## Single best next task

Run a target-host **worker CPU-isolation experiment** against the exact accepted source candidate, without source changes and without touching production services/data.

Use the same disposable PostgreSQL/API seed, same 8192x8192 image, same around-post HTTP benchmark, concurrency 8, and matched sequential rounds. Compare at least:

1. same-run no-worker baseline;
2. current unisolated one-worker behavior;
3. worker with reduced cgroup CPU scheduling weight / Docker CPU shares (preferred first mitigation because it yields CPU under contention but can use idle capacity);
4. a 0.5-CPU worker quota as a control if weight alone does not protect API latency.

Use at least 5 matched rounds per condition because the final gate showed meaningful round variance. Record p50/p95/p99/max/RPS/errors plus worker image time, CPU, RSS, thread count, API CPU/RSS, PostgreSQL CPU/RSS/connections, and exact cgroup settings. Inspect CPU topology/sibling layout as read-only context. Do not tune production and do not change codec quality/speed/thread constants.

Goal: find the smallest scheduling/resource-isolation policy that returns API latency/throughput close to same-run baseline without introducing unacceptable worker latency. If resource isolation solves the interference, document the required deployment constraint, commit the accepted `Cargo.lock`, rerun a short exact-head correctness/performance confirmation, and only then integrate into `v2`. If it does not, profile decode/resize/encode CPU phases before changing image algorithms or codec settings.
