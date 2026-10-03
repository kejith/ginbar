# Ginbar v2 State / Handoff

Last updated: 2026-10-03
Phase: M3 media pipeline — STILL-IMAGE PROCESSING INTEGRATED; PRODUCTION WORKER LOOP NEXT
Integration branch: `v2`
Active feature branch: `astra/m3-image-processing` (image slice complete; may be retired after integration)
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the resume point. Read it before `PLAN.md`. Do not rely on chat history as project memory.

## Branch / milestone status

- `master` is legacy and must remain untouched at `181fa44d79c7b4a1984c1a35795762dd503b3f77`.
- The still-image processing slice was developed from `v2` base `e601c78486d219f97f98e55349209849f79c353f` and is accepted for integration.
- M1 board benchmark is complete.
- M2 fresh schema + core Go API is complete and integrated.
- M3 durable PostgreSQL media-job claim/retry/recovery is integrated.
- M3 upload/URL ingestion + durable enqueue is integrated.
- M3 Rust source-consumption + generation-fenced DB publication is integrated.
- M3 still-image decode/resize/AVIF/thumbnail + durable no-overwrite file publication is now accepted and integrated.
- M3 is **not complete**. Remaining work includes the long-running worker loop, lease renewal/cancellation, video processing, duplicate detection, regeneration, progress/status UI, and a production-runner background-load gate.

## Retained architecture / invariants

Keep unless new evidence contradicts them:

- Go 1.25 + pgx/v5 + standard `net/http` for API/workflows;
- PostgreSQL authoritative for application state and durable media jobs;
- Rust media worker;
- local NVMe media storage;
- no Redis unless a measured need appears;
- immutable numeric relational IDs;
- primary feed uses post-ID cursor pagination, never OFFSET;
- durable jobs use PostgreSQL ownership + generation fencing;
- codec/filesystem work stays outside DB transactions;
- publication is fenced by owner/generation, source identity, post eligibility, and lease validity;
- at-least-once processing requires deterministic/idempotent durable side effects;
- initial still-image worker concurrency remains **one job at a time**;
- AVIF encoder thread count remains **one** until new measurements justify more.

## Accepted pre-image baselines

Ingestion:

- 8 MiB durable source staging: **45.1468 ms/op**, **186.06 MB/s**;
- PostgreSQL ingestion CTE c1: **0.348 ms**, **2,870.87 TPS**;
- PostgreSQL ingestion CTE c4: **0.457 ms**, **8,761.36 TPS**.

Source verification, target host, release mode:

- 8 MiB: **35.104 ms**, **227.91 MiB/s**;
- 64 MiB: **298.757 ms**, **214.92 MiB/s**;
- 256 MiB: **1,122.598 ms**, **228.05 MiB/s**.

Fenced publication SQL, PostgreSQL 17.11:

- 1,000/1,000 successful publications;
- **1.539 ms** average statement latency;
- **644.22 TPS**;
- bounded/indexed one-row plan: **0.949 ms**.

Evidence: `m3-worker-processing-contract-final-20261003T013900Z.zip`, SHA-256 `14DA7E1C4B8FB5495EFA6D2C1C920FD01CCF724F7441EFE5A928887216238ACB`, tested SHA `37ba916fad6e265f8e8ffde7b742533f8c55425a`.

## Integrated still-image processing contract

Worker v2 lives under `src/worker/v2`; legacy `src/worker` remains reference-only.

### Durable output publication

`src/worker/v2/src/output.rs`:

- confines output below `media/`;
- rejects symlink/non-directory output path components;
- uses same-directory `create_new` staging;
- fully writes + fsyncs staged files;
- publishes with no-overwrite same-filesystem hard links;
- fsyncs the containing directory;
- cleans staging and fsyncs directory again;
- verifies exact size + SHA-256 for idempotent reuse;
- treats differing bytes at an existing deterministic destination as terminal and never overwrites them.

Crash tests cover failure after staging fsync and after final-directory fsync. Hidden staging orphans remain future janitor/reconciliation work.

### Bounded still-image processor

`src/worker/v2/src/image.rs` processing version 1 supports JPEG, non-animated PNG, and non-animated WebP.

Animated PNG/WebP, GIF, AVIF/HEIF input, and video are intentionally terminal/out-of-scope for this processing version.

Output-affecting contract:

- full decode before durable publication;
- orientation applied and dimensions revalidated;
- max input dimension 16,384 px;
- max decoded pixels 80,000,000;
- decoder allocation hint 384 MiB;
- canonical output fits within 1,280 px without upscale;
- 256x256 center-crop thumbnail;
- full decode dropped after main-image downsize;
- both AVIF outputs encoded before either is published;
- `ravif` speed 10;
- main quality 75;
- thumbnail quality 60;
- AVIF encoder threads = 1;
- deterministic canonical `.avif` + `<stem>.thumb.avif`;
- canonical SHA-256/size/dimensions feed the already validated fenced DB publication.

Any output-affecting change requires incrementing `PROCESSING_VERSION`.

`process-once` handles at most one job and is a validated execution primitive, not the eventual polling/lease-renewing runner.

## Correctness acceptance

Accepted source semantics were established at `e53605838075509800efeaa644319d29e18587f2`; later pre-lock feature commits changed state/history only.

Final correctness evidence: `m3-image-processing-final-20261003T143503Z.zip`, SHA-256 `2DC7980BC81342E770768DD4F460484F422B549E025ACA8D1EAAFABA135E9045`.

Target/toolchain: Ubuntu 24.04.3, Linux 6.8.0-88-generic, Intel i7-7700 (4 physical / 8 logical CPUs), ext4 RAID1 NVMe, PostgreSQL 17.11, Rust/Cargo 1.99.0.

Accepted results:

- rustfmt/check/Clippy `-D warnings`: pass;
- no-DB tests: pass;
- PostgreSQL-enabled suite: pass;
- explicit image crash/idempotency/collision tests: pass;
- release worker + `image_bench`: pass;
- deterministic DB/filesystem publication + hash checks: pass;
- collision/no-overwrite behavior: pass.

Real-media processing medians:

- 172x178 JPEG: **79.145 ms**;
- 10109x4542 JPEG: **1,292.111 ms**, output 1280x575;
- 800x600 PNG: **259.259 ms**;
- 550x368 WebP: **170.883 ms**;
- deterministic 8192x8192 PNG: standalone process **1,923.785 ms**, ~370 MiB max RSS.

## Dependency reproducibility / final lock gate

The application lockfile is now committed at `src/worker/v2/Cargo.lock`.

Exact accepted bytes:

- size: **40,521 bytes**;
- SHA-256: `4B355C9016EF56D71C78D4CFCC347BF0A6FD2F3DBA8E6F36465B6D3EDEDAA74A`;
- Git blob SHA-1: `9bd2933d1a25f84cb04dc354476a3e6c7cdb0619`.

An unconstrained fresh resolution had drifted only `mio 1.2.3 -> 1.2.4`; the committed lock freezes the graph used by the accepted measurements.

Final pushed lock commit before this state commit:

`83dac34f399c52d3b0be7f94cfe7ffb1865edb79`

Parent:

`b6d7164068a9a7c2a6f305bbe4a39f2f765f3f18`

That commit changes exactly `src/worker/v2/Cargo.lock`; remote GitHub reports the expected blob SHA above.

Final lock-validation handoff evidence:

- `cargo check --locked`: pass;
- `cargo test --locked`: pass, **29 tests passed**;
- `cargo clippy --locked --all-targets -- -D warnings`: pass;
- Rust/Cargo 1.99.0, Clippy 0.1.99;
- lock bytes/hash/blob unchanged after tests;
- pushed without force;
- `v2` and `master` unchanged during the push;
- isolated worktree clean.

Evidence archive reported by the local agent: `m3-image-processing-lock-final-20261003T181052Z.zip`, SHA-256 `64680d382ad787972cfab61162805979201da357bf4313171b724ea064514527`, 14 manifest-listed payloads verified.

## API coexistence / performance decision

Final load-sensitivity evidence: `m3-image-processing-load-curve-final-20261003T174724Z.zip`, SHA-256 `C2432D3B4E38A8ADFC842DE2205903BEE0C31B9A3EC1D26F9AD4DF13CF80DD13`.

Evidence quality:

- manifest verified 823/823 payload files;
- locked check/test/Clippy passed;
- five baseline + five worker-overlap rounds at each c1/c2/c4/c8;
- 40/40 primary HTTP rounds valid;
- 20/20 worker fixtures valid;
- zero HTTP errors;
- DB/filesystem integrity passed for every loaded fixture.

Median baseline -> loaded:

| concurrency | baseline p95 | loaded p95 | p95 change | baseline p99 | loaded p99 | RPS change |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| c1 | 2.329 ms | 2.427 ms | +4.21% | 2.576 ms | 2.623 ms | -7.87% |
| c2 | 2.550 ms | 2.621 ms | +2.78% | 2.836 ms | 2.862 ms | -6.03% |
| c4 | 2.853 ms | 3.199 ms | +12.12% | 3.298 ms | 4.420 ms | -4.75% |
| c8 | 4.665 ms | 5.888 ms | +26.22% | 5.693 ms | 7.177 ms | -12.65% |

At c8, median loaded max latency was **10.949 ms** and all measured requests succeeded. Baseline API+PostgreSQL CPU medians were ~1.13, 2.10, 3.86, and 6.16 logical CPUs at c1/c2/c4/c8. Worker CPU remained ~1.12-1.15 logical CPUs and worker wall time ~2.57-2.82 s.

Decision for this image slice:

- accept one image job at a time;
- accept one AVIF encoder thread;
- do not increase image concurrency to the earlier PLAN hypothesis of ~2 without new measurements;
- do not add scheduler/load-admission complexity until the real continuous worker exists and can be measured;
- retain the documented peak-capacity tax as a production/deployment constraint.

## Historical scheduling experiments

Unrestricted c8, low cgroup weight, 0.5-CPU quota, and safe cpuset partitioning all showed material peak-capacity interference. Low weight did not help; 0.5 CPU slowed the worker substantially; reserving a physical core had its own large API capacity tax. `perf` profiling was blocked by host `perf_event_paranoid=4`; no host security setting was changed.

These runs are capacity evidence, not justification for speculative image-path micro-optimization.

## Deferred observations

- Full source SHA-256 verification costs one sequential read before codec consumption; do not redesign without profiler evidence.
- Current canonicalization/symlink checks are not `openat2`/`O_NOFOLLOW` race-proof against a malicious concurrent local writer; the media tree is service-controlled.
- Ingestion source orphans and processed staging orphans need eventual janitor/reconciliation work.
- Deployment must ensure API and worker share media storage with compatible UID/GID/permissions.
- Video needs its own explicit thread/concurrency and API-interference benchmark.

## Remaining M3 work

- long-running worker polling/wakeup loop;
- lease renewal while processing;
- cancellation/lost-ownership handling during long codec work;
- video processing;
- perceptual duplicate detection;
- regeneration jobs;
- progress/status UI;
- production-level background workload gate for the configured continuous worker.

## Local-agent evidence workflow

Use isolated/disposable resources. Local agent is execution/read-only by default and must not modify source/docs/config/SQL/commits/branches/deployments/persistent state without explicit user approval for the specific write.

Return one evidence ZIP with exact SHA/status, commands, stdout/stderr, environment versions, measurements, invalid attempts, and cleanup proof. Preserve raw remote evidence until reviewed.

## Single best next task

Start the **production worker loop + lease-renewal/cancellation slice** from current `v2` after verifying the integration ref.

Design the smallest coherent runner around the already integrated one-job processing primitive:

1. claim at most one job at a time;
2. poll/wakeup without busy-spinning;
3. renew the lease during source verification/codec/filesystem work;
4. cancel/abort publication promptly when ownership is lost or cancellation/deadline fires;
5. keep DB transactions short and codec work outside transactions;
6. preserve deterministic/idempotent output semantics;
7. use bounded retry/backoff and graceful shutdown;
8. test crash/restart, lease expiry/loss, renewal failure, cancellation, and shutdown;
9. benchmark the real continuous runner against API latency before adding scheduler/admission complexity.

Do not begin video processing until the production runner boundary is validated.