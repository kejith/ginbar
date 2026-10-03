# Ginbar v2 State / Handoff

Last updated: 2026-10-03
Phase: M3 image-processing slice — CORRECTNESS ACCEPTED; LOAD-SENSITIVITY GATE BLOCKED BY UNCOMMITTED LOCK DRIFT
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
- M3 durable PostgreSQL media-job ownership/recovery is integrated.
- M3 upload/URL-ingestion + durable enqueue is integrated.
- M3 Rust source-consumption/fenced-publication contract is integrated.
- Image-processing source candidate accepted for correctness/release behavior: `e53605838075509800efeaa644319d29e18587f2`, based exactly on `v2` `e601c78486d219f97f98e55349209849f79c353f`.
- Later feature-branch commits are state/history-only; image-processing source semantics have not changed since `e536058...`.
- Do **not** merge to `v2` yet. Realistic API/worker coexistence still needs a load-sensitivity measurement.
- Worker v2 lives under `src/worker/v2`; legacy `src/worker` remains reference-only.
- `.local-agent-results/` remains ignored for evidence ZIPs.
- No `src/worker/v2/Cargo.lock` is currently committed. Multiple transient lockfile import attempts were reverted after byte-identity verification failed; net tree effect is zero. Use the exact lock bytes retained in evidence, not hand-transcribed content.

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
- do not weaken AVIF quality/output semantics from saturation evidence alone;
- a max-throughput closed-loop benchmark is capacity evidence, not by itself a realistic zero-interference service-level requirement on a fully utilized 4-core host.

## Integrated performance baselines

Accepted ingestion:

- 8 MiB durable source staging: **45.1468 ms/op**, **186.06 MB/s**;
- PostgreSQL ingestion CTE c1: **0.348 ms**, **2,870.87 TPS**;
- PostgreSQL ingestion CTE c4: **0.457 ms**, **8,761.36 TPS**.

Accepted source verification, target host, release mode, warm cache:

- 8 MiB: **35.104 ms**, **227.91 MiB/s**;
- 64 MiB: **298.757 ms**, **214.92 MiB/s**;
- 256 MiB: **1,122.598 ms**, **228.05 MiB/s**.

Accepted fenced-publication SQL, PostgreSQL 17.11:

- 1,000/1,000 successful publications;
- **1.539 ms** average statement latency;
- **644.22 TPS**;
- bounded indexed plan; one-row execution **0.949 ms**.

Validated source/publication evidence: `m3-worker-processing-contract-final-20261003T013900Z.zip`, SHA-256 `14DA7E1C4B8FB5495EFA6D2C1C920FD01CCF724F7441EFE5A928887216238ACB`, tested SHA `37ba916fad6e265f8e8ffde7b742533f8c55425a`.

## Image-processing candidate

### Durable output publication

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

Final correctness evidence: `m3-image-processing-final-20261003T143503Z.zip`, SHA-256 `2DC7980BC81342E770768DD4F460484F422B549E025ACA8D1EAAFABA135E9045`, exact tested source `e53605838075509800efeaa644319d29e18587f2`.

Target/toolchain: Ubuntu 24.04.3, Linux 6.8.0-88-generic, Intel i7-7700 (4 physical / 8 logical CPUs), ext4 RAID1 NVMe, PostgreSQL 17.11, Rust/Cargo 1.99.0.

Accepted results:

- rustfmt/check/Clippy `-D warnings`: pass;
- no-DB Rust tests: 34 passed;
- PostgreSQL-enabled full suite: 29 passed;
- explicit `image_pipeline`: 2 passed;
- release worker and `image_bench`: pass;
- crash-before-DB retry/idempotency: pass;
- deterministic collision preservation/no DB release: pass;
- end-to-end jobs clear ownership, release posts, publish one canonical row plus canonical/thumbnail files, and match DB/filesystem SHA-256.

Accepted codec/resource measurements:

- 172x178 JPEG: process median **79.145 ms**;
- 10109x4542 JPEG: process median **1,292.111 ms**, output 1280x575;
- 800x600 PNG: process median **259.259 ms**;
- 550x368 WebP: process median **170.883 ms**;
- deterministic 8192x8192 PNG (67.1 MP): standalone process **1,923.785 ms**, wall 1.93 s, max RSS **369,844 KiB**.

## Dependency-lock evidence and current drift

The correctness/performance runs repeatedly used a generated `Cargo.lock` with SHA-256:

`4B355C9016EF56D71C78D4CFCC347BF0A6FD2F3DBA8E6F36465B6D3EDEDAA74A`

The core-isolation evidence ZIP contains the exact 40,521-byte lockfile with that SHA-256.

The blocked load-curve attempt generated a fresh 40,521-byte lockfile from the same accepted source and Rust/Cargo 1.99.0, but current registry resolution produced SHA-256:

`00E6C634CCFFBAC6A25DC0CDDBC00A2A58E3FE05AB4AF639412BDAFE9AE362DB`

Byte/parsed comparison of the two evidence lockfiles shows exactly one dependency-version difference:

- accepted graph: `mio 1.2.3`;
- current fresh resolution: `mio 1.2.4`.

All other package/version sets are identical. In this graph `tokio` is the package that depends on `mio`; direct image/codec pins (`image = 0.25.10`, `ravif = 0.13.0`, `rgb = 0.8.52`) did not move. Therefore the blocked run demonstrates transitive registry-resolution drift caused by the absence of a committed application lockfile, not source drift or codec-version drift.

For deterministic continuation, reconstruct the previously measured graph in a disposable clone by generating a lock then running `cargo update -p mio --precise 1.2.3`, and require the full lock SHA to become exactly `4B355C...AA74A` before any build/benchmark. Use `--locked` thereafter. Do not waive the SHA gate.

Before final integration, commit the exact accepted lock bytes through a write path that can verify the resulting Git blob/content identity, then rerun `cargo check --locked` and tests.

## API interference history

### Unisolated saturation gate

At concurrency 8 / 2,000 requests, one worker changed median p95 **+23.89%** and throughput **-11.73%**. Worker used roughly one CPU and ~370 MiB RSS.

### CPU weight/quota experiment

Evidence: `m3-image-processing-cpu-isolation-20261003T152345Z.zip`, SHA-256 `C95822B7C84AFFDF69D85AC9899E69D6169B88F0A12AA090B8B54D27B9168A04`.

- unrestricted: p95 **+32.37%**, throughput **-15.79%**;
- low weight (`cpu.weight=10`): p95 **+32.64%**, throughput **-14.96%**;
- 0.5 CPU quota: p95 **+24.10%**, throughput **-10.81%**, while standalone worker time rose from 2.04 s to 7.53 s.

Weight/quota are not acceptable final policies.

### Physical-core cpuset experiment

Evidence: `m3-image-processing-core-isolation-20261003T155243Z.zip`, SHA-256 `316CC0036675A0E54C6A0E0F4922EF39A9323A2AA5044009F55932FDE45E40B6`.

Median five-round results:

| Condition | p95 | throughput |
| --- | ---: | ---: |
| full-host baseline | 4.464 ms | 2,518.1 req/s |
| reserved-core baseline | 5.404 ms | 2,242.1 req/s |
| pin-only worker | 6.846 ms | 1,884.3 req/s |
| partitioned worker | 6.630 ms | 2,005.1 req/s |

- reservation tax: p95 **+21.04%**, throughput **-10.96%**;
- worker interference after reservation: p95 **+22.70%**, throughput **-10.57%**;
- pin-only: p95 **+53.35%**, throughput **-25.17%**.

This safely isolated only disposable Ginbar components; unrelated host workloads remained free to schedule. The reservation tax alone proves no spare physical core exists for Ginbar at the closed-loop saturation point.

### Profiling attempt

`perf_event_paranoid=4` denied `perf stat` and `perf record` even with container-scoped `CAP_PERFMON`. No host security setting was changed. No function-level hotspot attribution is available; do not claim a decode/resize/encode/hash/allocator hotspot from these runs.

## Blocked load-sensitivity attempt

Evidence ZIP: `m3-image-processing-load-curve-20261003T164348Z.zip`.
ZIP SHA-256: `89B4E9D04DF0E3361DD38D43BF3B6472BD1FA0929C0466D08938B9CE6423AA63`.
Feature HEAD tested: `0965b03f943035201492de814eba2e10a7f9cd12`; accepted processing source remained `e536058...`.

The run stopped correctly at the mandatory lock SHA gate before release builds, PostgreSQL/API/worker containers, calibration, or HTTP measurements. The fresh lock mismatch reproduced in a second clean lock-only clone. The ZIP preserves the mismatching lockfile and verifies 47 payload files. Cleanup preserved all 21 pre-existing containers and 14 networks.

No load-threshold/admission-policy conclusion can be drawn from this attempt.

## Remaining observations / deferred work

- Full source SHA-256 verification costs one sequential read before codec consumption; do not redesign without profile evidence.
- Standard-library canonicalization/symlink checks are not `openat2`/`O_NOFOLLOW` race-proof against a malicious concurrent local writer; current media tree is service-controlled.
- Ingestion source orphans and processed staging orphans need eventual janitor/reconciliation work.
- The one-shot worker does not renew leases; production polling needs measured renewal/cancellation behavior later in M3.
- Deployment must ensure API and worker share media storage with compatible UID/GID/permissions.

## Local-agent evidence workflow

For target-host work, use isolated/disposable resources and return one ZIP containing exact SHA/status, commands, raw stdout/stderr, environment/tool versions, measurements, invalid attempts, and cleanup/restoration evidence. Preserve raw remote evidence until reviewed.

## Single best next task

Rerun the **API-load sensitivity / worker coexistence curve** against the same accepted source, but deterministically reconstruct the measured dependency graph before the gate:

1. fresh disposable clone/detached accepted source;
2. Rust/Cargo 1.99.x;
3. `cargo generate-lockfile`;
4. `cargo update -p mio --precise 1.2.3`;
5. verify full `Cargo.lock` SHA-256 equals exactly `4B355C9016EF56D71C78D4CFCC347BF0A6FD2F3DBA8E6F36465B6D3EDEDAA74A`;
6. use `cargo check --locked`, `cargo test --locked`, and all release builds with `--locked`;
7. only if those pass, run the planned c1/c2/c4/c8 baseline-versus-one-worker load curve with at least five interleaved valid pairs per concurrency, CPU/headroom/PSI evidence, worker timings, and full integrity checks.

Do not generate a new unconstrained dependency graph after the SHA check. Do not modify source semantics. The purpose is to determine whether interference is headroom-sensitive at realistic lower API loads or persists despite substantial measured CPU headroom.
