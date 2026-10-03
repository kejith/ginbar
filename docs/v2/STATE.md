# Ginbar v2 State / Handoff

Last updated: 2026-10-03
Phase: M3 image-processing slice — CORRECTNESS PASSED; PEAK-LOAD CPU COEXISTENCE UNSOLVED; LOAD-SENSITIVITY GATE NEXT
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
- Image-processing source candidate accepted for correctness/release behavior: `e53605838075509800efeaa644319d29e18587f2`, based exactly on `v2` `e601c78486d219f97f98e55349209849f79c353f`.
- Later feature-branch commits are state/history-only; image-processing source semantics have not changed since `e536058...`.
- Do **not** merge to `v2` yet. The current target host cannot demonstrate acceptable coexistence between the CPU-heavy worker and the API at the existing closed-loop saturation benchmark.
- Worker v2 lives under `src/worker/v2`; legacy `src/worker` remains reference-only.
- `.local-agent-results/` remains ignored for evidence ZIPs.

Two transient feature-branch commits attempted then reverted a `Cargo.lock` import because the primary session could not guarantee byte-for-byte identity through that write path. Net tree effect is zero. The exact validated lock bytes remain in the core-isolation evidence ZIP and are not yet committed.

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
- worker scheduling/resource policy must protect API latency under realistic concurrent load; a max-throughput saturation benchmark is useful capacity evidence but should not by itself define an impossible zero-interference requirement on a fully utilized 4-core host.

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

Resolved dependency lock SHA-256 repeatedly equals `4B355C9016EF56D71C78D4CFCC347BF0A6FD2F3DBA8E6F36465B6D3EDEDAA74A`.

The core-isolation evidence ZIP now contains the exact generated `Cargo.lock` bytes with that SHA-256. Commit those exact bytes only through a path that can verify byte identity afterward, then rerun `cargo check --locked`/tests before integration.

Accepted codec/resource measurements, five iterations each:

- 172x178 JPEG: process median **79.145 ms**;
- 10109x4542 JPEG: process median **1,292.111 ms**, output 1280x575;
- 800x600 PNG: process median **259.259 ms**;
- 550x368 WebP: process median **170.883 ms**;
- deterministic 8192x8192 PNG (67.1 MP): standalone process **1,923.785 ms**, wall 1.93 s, max RSS **369,844 KiB**.

## API interference history

### Unisolated saturation gate — BLOCKING AS PEAK-CAPACITY EVIDENCE

Existing `httpbench.go`, `/api/v2/posts/49999/around?radius=30`, concurrency 8, 2,000 requests, three baseline + three valid worker-overlap rounds:

| Metric | Baseline | Worker | Change |
| --- | ---: | ---: | ---: |
| p50 | 3.071 ms | 3.557 ms | +15.81% |
| p95 | 4.733 ms | 5.864 ms | **+23.89%** |
| p99 | 5.678 ms | 6.884 ms | **+21.24%** |
| throughput | 2,386.6 req/s | 2,106.6 req/s | **-11.73%** |

Worker used roughly one CPU and ~370 MiB RSS.

### CPU weight/quota experiment — NOT SUFFICIENT

Evidence ZIP: `m3-image-processing-cpu-isolation-20261003T152345Z.zip`, SHA-256 `C95822B7C84AFFDF69D85AC9899E69D6169B88F0A12AA090B8B54D27B9168A04`.

Five balanced rounds per policy, exactly one active worker job during valid loaded intervals, 24/24 integrity fixtures passed.

| Policy | p95 vs baseline | p99 vs baseline | throughput vs baseline |
| --- | ---: | ---: | ---: |
| unrestricted | **+32.37%** | +46.28% | **-15.79%** |
| low weight (`cpu.weight=10`) | **+32.64%** | +37.91% | **-14.96%** |
| 0.5 CPU quota | **+24.10%** | +25.20% | **-10.81%** |

Low scheduling weight was ineffective. The 0.5-CPU hard cap still left material interference and stretched the standalone 8192x8192 job from **2.04 s** to **7.53 s** (3.69x slower).

### Physical-core cpuset experiment — ALSO NOT SUFFICIENT AT SATURATION

Evidence ZIP: `m3-image-processing-core-isolation-20261003T155243Z.zip`.
ZIP SHA-256: `316CC0036675A0E54C6A0E0F4922EF39A9323A2AA5044009F55932FDE45E40B6`.
Experiment feature HEAD: `411b3c1d1f1ddf57d09f614156920be3427054fd`; processing source remained `e536058...`.

Evidence quality:

- embedded SHA-256 manifest verified all 442 payload files;
- exact generated `Cargo.lock` recovered and verified at accepted SHA-256 `4B355C...AA74A`;
- all 20 balanced primary rounds passed validity gates;
- all 13 worker fixtures passed integrity;
- all ten loaded primary rounds kept exactly one active job through the HTTP interval;
- pre-existing Docker/container/network state was restored; raw remote evidence retained.

Conditions:

- A: API/PG `0-7`, no worker;
- B: API/PG `0-2,4-6`, no worker (reserve physical core `3/7`);
- C: API/PG `0-7`, worker `3,7`;
- D: API/PG `0-2,4-6`, worker `3,7`.

Median five-round results:

| Condition | p95 | p99 | throughput |
| --- | ---: | ---: | ---: |
| A full-host baseline | 4.464 ms | 5.410 ms | 2,518.1 req/s |
| B reserved-core baseline | 5.404 ms | 6.463 ms | 2,242.1 req/s |
| C pin-only worker | 6.846 ms | 8.444 ms | 1,884.3 req/s |
| D partitioned worker | 6.630 ms | 8.056 ms | 2,005.1 req/s |

Separate effects:

- reservation tax B vs A: p95 **+21.04%**, p99 +19.46%, throughput **-10.96%**;
- worker interference D vs B: p95 **+22.70%**, p99 +24.64%, throughput **-10.57%**;
- pin-only C vs A: p95 **+53.35%**, throughput **-25.17%**.

Standalone worker time did not worsen from pinning: 2.20 s on all logical CPUs versus 2.08 s on `3,7`; peak thread count fell from 11 to 5.

Important limitation: this was safe disposable isolation of the Ginbar API/PG/worker only. Existing unrelated host workloads were intentionally left untouched and remained free to schedule on the host, so `3,7` was not an exclusive whole-host physical core. Nevertheless the reservation baseline alone demonstrates that taking one physical core away from Ginbar API/PG costs materially under this saturation test before the media worker even runs.

### Profiling attempt — BLOCKED BY HOST POLICY

The physical-core experiment triggered profiling. Input hashes for the 8192x8192 PNG and Fronalpstock JPEG were verified.

`perf stat`, `perf record -g`, and `perf report --stdio` were attempted read-only with container-scoped `CAP_PERFMON`. Kernel `perf_event_paranoid=4` denied access; both record attempts produced zero-byte data. No host sysctl/capability/security setting was changed.

Therefore there is **no evidence-backed function-level hotspot attribution**. Do not claim decode, resize, RGBA conversion, rav1e, hashing, allocator, or filesystem work is the dominant CPU hotspot from these runs.

## Current interpretation

The image-processing implementation is correctness-accepted. Three different coexistence mechanisms (normal scheduling, low cgroup weight/hard quota, and safe cpuset partitioning) all show material API degradation under the existing concurrency-8 closed-loop benchmark.

That benchmark is also effectively a peak-capacity test: full-host API/PG reaches ~2.5k req/s, and merely removing one of four physical cores reduces throughput ~11% and p95 ~21%. On this host there is no spare physical core at that offered load.

Accordingly, do not micro-optimize the image path based on the failed saturation coexistence test, and do not weaken AVIF quality/output semantics. The next architectural question is whether worker interference remains material at lower API offered loads where actual CPU headroom exists. That determines whether a load-aware admission policy can safely share this host, or whether worker capacity must be separated from latency-sensitive API capacity.

## Remaining observations / deferred work

- Full source SHA-256 verification costs one sequential read before codec consumption; do not redesign without profile evidence.
- Standard-library canonicalization/symlink checks are not `openat2`/`O_NOFOLLOW` race-proof against a malicious concurrent local writer; current media tree is service-controlled.
- Ingestion source orphans and processed staging orphans need eventual janitor/reconciliation work.
- The one-shot worker does not renew leases; production polling needs measured renewal/cancellation behavior later in M3.
- Deployment must ensure API and worker share media storage with compatible UID/GID/permissions.
- Do not lower AVIF quality, output dimensions, or format merely to satisfy a saturated-host benchmark without profiler or load-curve evidence.

## Local-agent evidence workflow

For target-host work, use isolated/disposable resources and return one ZIP containing exact SHA/status, commands, raw stdout/stderr, environment/tool versions, measurements, invalid attempts, and cleanup/restoration evidence. Preserve raw remote evidence until reviewed.

## Single best next task

Run a target-host **API-load sensitivity / worker coexistence curve** with no source changes and no production configuration changes.

Use the existing `src/backend/v2/bench/httpbench.go`; do not add a new load generator yet. Vary closed-loop HTTP concurrency to create different CPU/headroom regimes while keeping the same endpoint and disposable data:

1. concurrency 1;
2. concurrency 2;
3. concurrency 4;
4. concurrency 8 (existing saturation reference).

For each concurrency, collect at least five interleaved no-worker baseline rounds and five valid one-worker-overlap rounds using the accepted 8192x8192 PNG, with request counts chosen so the HTTP interval remains shorter than the worker job and overlap is provable. Preserve the same integrity/job-overlap checks used by prior gates.

Record p50/p95/p99/max/RPS/errors plus API/PG/worker CPU and RSS, PostgreSQL connections, host CPU utilization/pressure if readable, and worker wall time. Calculate worker-vs-baseline deltas at each concurrency and relate them to measured baseline CPU headroom.

The goal is to locate the load/headroom point at which one worker begins to materially affect latency/throughput. If low/moderate concurrency shows small interference while c8 alone degrades materially, design the production worker runner around explicit load-aware admission/backoff rather than permanent CPU reservation or codec changes. If interference remains material even with substantial CPU headroom, the next architecture decision is separate worker capacity; only then revisit profiler access or image-pipeline optimization.

Do not merge to `v2` until this load-sensitivity evidence is reviewed and the production coexistence policy is explicit.