# Ginbar v2 State / Handoff

Last updated: 2026-10-03
Phase: M3 image-processing slice — CORRECTNESS PASSED; CPU-INTERFERENCE GATE STILL BLOCKS INTEGRATION
Integration branch: `v2`
Active feature branch: `astra/m3-image-processing`
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the resume point. Read it before `PLAN.md`. Do not rely on chat history as project memory.

## Branch status

- `master` remains untouched by rewrite work at `181fa44d79c7b4a1984c1a35795762dd503b3f77`.
- `v2` remains unchanged by image-processing work at `e601c78486d219f97f98e55349209849f79c353f`.
- M1 board benchmark is complete.
- M2 fresh schema + core Go API is complete and integrated.
- M3 durable PostgreSQL media-job ownership/recovery boundary is integrated.
- M3 upload/URL-ingestion + durable-enqueue boundary is integrated.
- M3 Rust worker source-consumption/fenced-publication contract is integrated.
- Image-processing source candidate accepted for correctness/release behavior: `e53605838075509800efeaa644319d29e18587f2` on `astra/m3-image-processing`, based exactly on `v2` `e601c78486d219f97f98e55349209849f79c353f`.
- Later commits on the feature branch are state-only; image-processing source semantics have not changed since `e536058...`.
- Do **not** merge to `v2` yet. Target-host CPU/API interference remains unresolved.
- Worker v2 lives under `src/worker/v2`; legacy `src/worker` remains reference-only.
- `.local-agent-results/` remains ignored for evidence ZIPs.

## Retained architecture / decisions

Keep unless evidence contradicts them:

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
- worker scheduling/resource policy must protect API latency under concurrent processing; one-job concurrency by itself is insufficient.

## Integrated performance baselines

Accepted ingestion:

- 8 MiB durable source staging: **45.1468 ms/op**, **186.06 MB/s**;
- PostgreSQL ingestion CTE c1: **0.348 ms**, **2,870.87 TPS**;
- PostgreSQL ingestion CTE c4: **0.457 ms**, **8,761.36 TPS**.

Accepted source verification, target host, release mode, warm cache:

- 8 MiB: **35.104 ms**, **227.91 MiB/s**;
- 64 MiB: **298.757 ms**, **214.92 MiB/s**;
- 256 MiB: **1,122.598 ms**, **228.05 MiB/s**.

Accepted fenced publication SQL, PostgreSQL 17.11:

- 1,000/1,000 successful publications;
- **1.539 ms** average statement latency;
- **644.22 TPS**;
- bounded indexed plan; one-row execution **0.949 ms**.

Validated source/publication evidence: `m3-worker-processing-contract-final-20261003T013900Z.zip`, SHA-256 `14DA7E1C4B8FB5495EFA6D2C1C920FD01CCF724F7441EFE5A928887216238ACB`, tested SHA `37ba916fad6e265f8e8ffde7b742533f8c55425a`.

## Image-processing candidate

### Durable no-overwrite publication

`src/worker/v2/src/output.rs` uses same-directory `create_new` staging, full file fsync, no-overwrite same-filesystem hard-link publication, containing-directory fsync, staging cleanup + second directory fsync, and exact size/SHA-256 verification for idempotent reuse. Deterministic-target byte mismatch is terminal and never overwrites existing bytes. Crash tests cover failure after staging fsync and after final-directory fsync.

### Bounded still-image processor

`src/worker/v2/src/image.rs` supports JPEG, non-animated PNG, and non-animated WebP. Animated PNG/WebP, GIF, AVIF/HEIF input, and video are terminal in processing version 1.

Processing-v1 contract:

- full decode before durable publication;
- orientation applied and dimensions revalidated;
- max input dimension 16,384 px;
- max decoded pixels 80,000,000;
- decoder allocation hint 384 MiB;
- canonical output fits within 1,280 px without upscale;
- 256x256 center-crop thumbnail via view + resize;
- full decode dropped after main-image downsize;
- both AVIFs encoded before either is published;
- `ravif` speed 10, main quality 75, thumbnail quality 60, explicit `with_num_threads(Some(1))`;
- deterministic `<stem>.thumb.avif` plus canonical `.avif`;
- canonical SHA-256/size/dimensions feed fenced DB publication.

Any output-affecting change after integration requires incrementing `PROCESSING_VERSION`.

`process-once` handles at most one job and is still a validation/execution probe, not the eventual polling/lease-renewing runner.

## Correctness / release acceptance

Final correctness evidence: `m3-image-processing-final-20261003T143503Z.zip`, SHA-256 `2DC7980BC81342E770768DD4F460484F422B549E025ACA8D1EAAFABA135E9045`, exact tested source candidate `e53605838075509800efeaa644319d29e18587f2`.

Target/toolchain: Ubuntu 24.04.3, Linux 6.8.0-88-generic, Intel i7-7700 (4 physical / 8 logical CPUs), ext4 RAID1 NVMe, PostgreSQL 17.11, Rust/Cargo 1.99.0.

Accepted correctness results:

- rustfmt/check/Clippy `-D warnings`: pass;
- no-DB Rust tests: 34 passed;
- PostgreSQL-enabled full suite: 29 passed;
- explicit `image_pipeline`: 2 passed;
- release worker and `image_bench`: pass;
- crash-after-files-before-DB retry/idempotency: pass;
- deterministic collision preservation/no DB release: pass;
- six end-to-end jobs: exactly-once ready media publication, cleared ownership, released posts, canonical/thumbnail files present, DB/filesystem SHA matching, unrelated sentinel unchanged.

Resolved dependency lock SHA-256 repeatedly equals `4B355C9016EF56D71C78D4CFCC347BF0A6FD2F3DBA8E6F36465B6D3EDEDAA74A`. The preceding corrective ZIP contains the actual lock bytes. Because this is an application binary and AVIF output/performance is dependency-sensitive, committing this exact `Cargo.lock` remains intended before integration, but not before the performance architecture is accepted.

Accepted codec/resource measurements, five iterations each:

- 172x178 JPEG: process median **79.145 ms**;
- 10109x4542 JPEG: process median **1,292.111 ms**, output 1280x575;
- 800x600 PNG: process median **259.259 ms**;
- 550x368 WebP: process median **170.883 ms**;
- deterministic 8192x8192 PNG (67.1 MP): standalone process **1,923.785 ms**, wall 1.93 s, max RSS **369,844 KiB**.

## API interference history

### Unisolated final gate — BLOCKING

Existing `httpbench.go`, `/api/v2/posts/49999/around?radius=30`, concurrency 8, 2,000 requests, three baseline + three valid worker-overlap rounds:

| Metric | Baseline | Worker | Change |
| --- | ---: | ---: | ---: |
| p50 | 3.071 ms | 3.557 ms | +15.81% |
| p95 | 4.733 ms | 5.864 ms | **+23.89%** |
| p99 | 5.678 ms | 6.884 ms | **+21.24%** |
| throughput | 2,386.6 req/s | 2,106.6 req/s | **-11.73%** |

Worker used roughly one CPU and ~370 MiB RSS. This blocked integration and led to the CPU-isolation experiment.

### CPU scheduling/isolation experiment — WEIGHT/QUOTA NOT SUFFICIENT

Evidence ZIP: `m3-image-processing-cpu-isolation-20261003T152345Z.zip`.
ZIP SHA-256: `C95822B7C84AFFDF69D85AC9899E69D6169B88F0A12AA090B8B54D27B9168A04`.
Source semantics remained the accepted `e536058...`; branch HEAD at experiment start was state-only `da6ddf4a53582cc602aa32a56ece73630d0aa5c4`.

Methodology:

- five interleaved baseline rounds;
- five balanced primary rounds each for unrestricted worker, low CPU weight, and 0.5-CPU quota;
- 2,000 requests per round, concurrency 8;
- exactly one active worker job before/after every valid loaded HTTP interval;
- 17 total valid loaded rounds retained in raw evidence (B=6, C=6, D=5);
- 24/24 media/job fixture integrity audits passed;
- all primary rounds had 2,000 HTTP 200 responses and zero errors;
- cleanup restored the pre-run Docker container/network inventory exactly.

Observed worker policies:

- B unrestricted: `cpu.weight=100`, no quota;
- C Docker `--cpu-shares 256`: actual cgroup v2 `cpu.weight=10`, no quota;
- D Docker `--cpus 0.5`: `cpu.max=50000 100000`, default weight.

Median API results across five balanced rounds:

| Policy | p95 | p95 vs baseline | p99 vs baseline | throughput vs baseline |
| --- | ---: | ---: | ---: | ---: |
| baseline | 4.761 ms | — | — | 2,402.6 req/s |
| unrestricted | 6.303 ms | **+32.37%** | +46.28% | **-15.79%** |
| low weight (`cpu.weight=10`) | 6.316 ms | **+32.64%** | +37.91% | **-14.96%** |
| 0.5 CPU quota | 5.909 ms | **+24.10%** | +25.20% | **-10.81%** |

Interpretation:

- Low scheduling weight did not materially protect p95 versus unrestricted processing.
- A 0.5-CPU hard cap reduced interference, but still left a material ~24% p95 regression.
- The 0.5-CPU cap also stretched the standalone 8192x8192 job from **2.04 s** unrestricted to **7.53 s** (3.69x slower), with 112 median throttled periods and ~11.8 s median throttled-usec across loaded rounds.
- Therefore neither tested policy is acceptable as the final production scheduling policy.

Critical CPU-topology/context finding:

- i7-7700 topology pairs logical CPUs by physical core as `0/4`, `1/5`, `2/6`, `3/7`.
- Under the saturated around-post benchmark, API samples commonly consume about **0.9-1.2 logical CPUs** while PostgreSQL consumes roughly **3.7-5.1 logical CPUs** before the media worker is added.
- The worker adds about **1.0-1.1 logical CPUs** unrestricted.
- The host is therefore already close to CPU saturation in this benchmark. CFS weight/quota changes logical CPU entitlement but do not create physical-core/SMT isolation, so the remaining interference is plausibly physical-core/SMT contention rather than an accidental multi-core worker bug.

Decision: **still do not integrate image processing into `v2`.** Source correctness is accepted; target-host CPU coexistence is not.

## Remaining observations / deferred work

- Full source SHA-256 verification costs one sequential read before codec consumption; do not redesign without profile evidence.
- Standard-library canonicalization/symlink checks are not `openat2`/`O_NOFOLLOW` race-proof against a malicious concurrent local writer; current media tree is service-controlled.
- Ingestion source orphans and processed staging orphans need eventual janitor/reconciliation work.
- The one-shot worker does not renew leases; production polling needs measured renewal/cancellation behavior later in M3.
- Deployment must ensure API and worker share media storage with compatible UID/GID/permissions.
- Do not lower AVIF quality, change output dimensions, change formats, or otherwise alter processing-v1 output semantics merely to satisfy the benchmark without first profiling where CPU time is spent.

## Local-agent evidence workflow

For target-host work, use isolated/disposable resources and return one ZIP containing exact SHA/status, commands, raw stdout/stderr, environment/tool versions, measurements, invalid attempts, and cleanup/restoration evidence. Preserve raw remote evidence until reviewed.

## Single best next task

Run one target-host **physical-core cpuset isolation experiment**, still with no source changes and no production configuration changes.

The experiment must compare:

1. full-host baseline: API + PostgreSQL allowed CPUs `0-7`, no worker;
2. reserved-core baseline: API + PostgreSQL restricted to three physical cores (`0,1,2,4,5,6`), no worker;
3. pin-only control: API + PostgreSQL still `0-7`, worker restricted to the fourth physical-core sibling pair (`3,7`);
4. true dedicated-core partition: API + PostgreSQL restricted to `0,1,2,4,5,6`, worker restricted to `3,7`.

Use at least five interleaved valid rounds per condition with the same 8192x8192 input, around-post endpoint, concurrency 8, and 2,000 requests. Capture exact cpuset controls, CPU/RSS/threads, DB connections, job overlap/integrity, and worker standalone completion time.

Evaluate two costs separately:

- **reservation tax:** reserved-core baseline versus full-host baseline;
- **worker interference:** dedicated-core loaded versus reserved-core baseline.

A dedicated core is only useful if the reservation tax itself is tolerable and the loaded run remains close to its matched reserved baseline. Do not accept it merely because worker interference disappears after permanently sacrificing too much API capacity.

If dedicated physical-core partitioning still leaves material API degradation or an unacceptable reservation tax, stop further scheduling tweaks and perform read-only `perf stat` + `perf record/report` profiling of the accepted release source on both the 8192x8192 PNG and the large photographic JPEG. Profile decode/transform/resize/AVIF phases by symbol evidence before proposing any image-algorithm or codec change.

If dedicated-core partitioning succeeds, document the deployment cpuset constraint, commit the accepted exact `Cargo.lock`, run one short exact-head correctness + matched performance confirmation, then fast-forward/integrate into `v2` if the gate remains green.