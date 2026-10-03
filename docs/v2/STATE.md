# Ginbar v2 State / Handoff

Last updated: 2026-10-03
Phase: M3 image-processing slice — CORRECTNESS/PERFORMANCE ACCEPTED; EXACT LOCKFILE COMMIT + INTEGRATION PENDING
Integration branch: `v2`
Active feature branch: `astra/m3-image-processing`
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the resume point. Read it before `PLAN.md`. Do not rely on chat history as project memory.

## Branch safety / status

- `master` remains legacy/read-only at `181fa44d79c7b4a1984c1a35795762dd503b3f77`.
- `v2` remains unchanged by this image slice at `e601c78486d219f97f98e55349209849f79c353f`.
- The image-processing implementation was branched from that exact `v2` SHA.
- Accepted image-processing source semantics remain commit `e53605838075509800efeaa644319d29e18587f2`.
- Commits after `e536058...` on the feature branch are state/history-only or transient net-zero repository probes; no image-processing source semantics changed.
- Do not modify `master`.
- Do not fast-forward `v2` until the exact validated `Cargo.lock` bytes are committed and a locked correctness check passes on the resulting feature head.
- Worker v2 is under `src/worker/v2`; legacy `src/worker` is reference-only.
- `.local-agent-results/` remains ignored for local evidence ZIPs.

## Integrated milestones / boundaries

Already integrated into `v2`:

- M1 board benchmark;
- M2 fresh schema + core Go API;
- durable PostgreSQL media-job claim/retry/recovery boundary;
- upload/URL ingestion + durable enqueue boundary;
- Rust source-consumption + deterministic/fenced DB publication contract.

Retained architecture:

- Go 1.25 + pgx/v5 + standard `net/http`;
- PostgreSQL authoritative for app/workflow/job state;
- Rust media worker;
- local NVMe media storage;
- no Redis without measured need;
- immutable numeric relational IDs;
- primary feed uses post-ID cursor pagination;
- media jobs use PostgreSQL + generation-fenced ownership;
- filesystem/codec work stays outside DB transactions;
- final publication is fenced by job owner/generation, source identity, post eligibility, and real-time lease expiry;
- at-least-once processing requires deterministic/idempotent side effects.

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
- bounded indexed plan; one-row execution **0.949 ms**.

Validated source/publication evidence: `m3-worker-processing-contract-final-20261003T013900Z.zip`, SHA-256 `14DA7E1C4B8FB5495EFA6D2C1C920FD01CCF724F7441EFE5A928887216238ACB`, tested SHA `37ba916fad6e265f8e8ffde7b742533f8c55425a`.

## Image-processing implementation

### Durable local output publication

`src/worker/v2/src/output.rs`:

- output paths constrained below `media/`;
- symlink/non-directory components rejected;
- same-directory `create_new` staging;
- staged file fully written + file-fsynced;
- no-overwrite same-filesystem hard-link final publication;
- containing directory fsync after publish;
- staging cleanup + second directory fsync;
- exact size + SHA-256 verification for idempotent reuse;
- differing bytes at an existing deterministic destination are terminal and never overwritten.

Crash tests cover failure after staged-file fsync and after final-directory fsync. Hidden staging orphans remain a future janitor concern.

### Bounded still-image processor

`src/worker/v2/src/image.rs` supports JPEG, non-animated PNG, and non-animated WebP.

Processing version 1 intentionally treats animated PNG/WebP, GIF, AVIF/HEIF input, and video as terminal/out-of-scope.

Current output-affecting contract:

- full decode before publication;
- orientation applied and dimensions revalidated;
- max input dimension 16,384 px;
- max decoded pixels 80,000,000;
- decoder allocation hint 384 MiB;
- canonical image fits within 1,280 px without upscale;
- 256x256 center-crop thumbnail;
- original full decode dropped after main-image downsize;
- both AVIF outputs encode before either is published;
- `ravif` speed 10, main quality 75, thumbnail quality 60;
- explicit AVIF encoder thread count = 1;
- deterministic `<stem>.thumb.avif` + canonical `.avif`;
- canonical output SHA-256/size/dimensions feed the already-validated fenced DB publication.

Any output-affecting change after integration requires incrementing `PROCESSING_VERSION`.

`process-once` intentionally handles at most one job and is still a validation/execution probe, not the eventual polling/lease-renewing production runner.

## Correctness acceptance

Final correctness evidence: `m3-image-processing-final-20261003T143503Z.zip`, SHA-256 `2DC7980BC81342E770768DD4F460484F422B549E025ACA8D1EAAFABA135E9045`, tested source `e53605838075509800efeaa644319d29e18587f2`.

Target/toolchain: Ubuntu 24.04.3, Linux 6.8.0-88-generic, Intel i7-7700 (4 physical / 8 logical CPUs), ext4 RAID1 NVMe, PostgreSQL 17.11, Rust/Cargo 1.99.0.

Accepted:

- rustfmt/check/Clippy `-D warnings` pass;
- no-DB tests pass;
- PostgreSQL-enabled suite pass;
- explicit image crash/idempotency/collision DB tests pass;
- release worker + `image_bench` build pass;
- deterministic DB/filesystem publication + hash checks pass;
- unrelated existing bytes are never overwritten.

Accepted real-media processing medians:

- 172x178 JPEG: **79.145 ms**;
- 10109x4542 JPEG: **1,292.111 ms**, output 1280x575;
- 800x600 PNG: **259.259 ms**;
- 550x368 WebP: **170.883 ms**;
- deterministic 8192x8192 PNG: standalone process **1,923.785 ms**, ~370 MiB max RSS.

## Dependency reproducibility

The exact graph used by the accepted final measurements is represented by a 40,521-byte `Cargo.lock` with SHA-256:

`4B355C9016EF56D71C78D4CFCC347BF0A6FD2F3DBA8E6F36465B6D3EDEDAA74A`

The corrected load-curve run reconstructed this graph from crates.io with:

```text
cargo generate-lockfile
cargo update -p mio --precise 1.2.3
```

and then used `--locked` for check/test/Clippy/build.

A prior unconstrained fresh resolution changed only `mio 1.2.3 -> 1.2.4`, producing lock SHA `00E6C634CCFFBAC6A25DC0CDDBC00A2A58E3FE05AB4AF639412BDAFE9AE362DB`; that was registry-resolution drift, not source drift. This confirms an application lockfile is required.

The exact validated lock bytes are preserved in `m3-image-processing-load-curve-final-20261003T174724Z.zip` as `evidence/Cargo.lock` and `evidence/Cargo.lock.reconstructed`.

Do not hand-reconstruct or normalize this file. Commit the exact bytes and verify SHA-256 before integration.

## API coexistence evidence

### Saturation + scheduler experiments

Earlier c8 closed-loop measurements showed a real peak-capacity cost from one large image job. Low cgroup weight, a 0.5 CPU quota, and safe cpuset partitioning did not remove that cost; hard quota significantly slowed media processing. `perf` profiling was unavailable because target kernel `perf_event_paranoid=4` and no host security setting was changed.

Those experiments remain useful capacity evidence, but the c8 closed-loop benchmark is effectively a maximum-throughput test. It should not alone force scheduler/admission complexity when absolute API latency remains low.

### Final load-sensitivity curve — ACCEPT IMAGE-SLICE CONFIGURATION

Evidence ZIP: `m3-image-processing-load-curve-final-20261003T174724Z.zip`.
ZIP SHA-256: `C2432D3B4E38A8ADFC842DE2205903BEE0C31B9A3EC1D26F9AD4DF13CF80DD13`.

Evidence quality:

- embedded manifest verified **823/823** payload files;
- reconstructed accepted `Cargo.lock` SHA exactly `4B355C...AA74A`;
- `cargo check --locked`: pass;
- `cargo test --locked`: pass;
- `cargo clippy --locked --all-targets -- -D warnings`: pass;
- five baseline + five loaded primary rounds at each c1/c2/c4/c8;
- **40/40** primary HTTP rounds valid;
- **20/20** worker fixtures valid;
- zero invalid attempts;
- all HTTP requests successful, zero errors, HTTP 200;
- all loaded jobs remained active across the measured HTTP interval and then completed successfully;
- DB/filesystem publication integrity passed for every loaded fixture;
- cleanup restored all pre-existing container/network/volume/image inventories.

Median baseline -> loaded changes:

| concurrency | baseline p95 | loaded p95 | p95 change | baseline p99 | loaded p99 | RPS change |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| c1 | 2.329 ms | 2.427 ms | **+4.21%** | 2.576 ms | 2.623 ms | -7.87% |
| c2 | 2.550 ms | 2.621 ms | **+2.78%** | 2.836 ms | 2.862 ms | -6.03% |
| c4 | 2.853 ms | 3.199 ms | **+12.12%** | 3.298 ms | 4.420 ms | -4.75% |
| c8 | 4.665 ms | 5.888 ms | **+26.22%** | 5.693 ms | 7.177 ms | -12.65% |

At c8, median loaded max latency was **10.949 ms** and all 2,000 measured requests per round still succeeded. At c1/c2, p95/p99 impact remained small in absolute and relative terms; c4/c8 show the expected shrinking peak-capacity margin as offered concurrency approaches saturation.

Measured baseline API+PostgreSQL CPU medians were ~1.13, 2.10, 3.86, and 6.16 logical CPUs at c1/c2/c4/c8 respectively. Loaded worker CPU remained ~1.12-1.15 logical CPUs. Worker wall time remained roughly 2.57-2.82 s across points.

### Performance decision

For the still-image slice, the configured limit is accepted as:

- **one media job at a time**;
- **one AVIF encoder thread**;
- no automatic increase to the PLAN's earlier image-concurrency≈2 hypothesis without new measurements.

Reason: even at the c8 saturation reference, absolute loaded p95/p99 remained ~5.9/7.2 ms with zero errors, within the project's low-single-digit/low-tens hot-path target. The measured tradeoff is reduced peak HTTP capacity (~12.7% RPS at c8), not a catastrophic interactive-latency failure. At lower concurrency the tail-latency effect is substantially smaller.

Do not claim the worker is free: capacity tax and shared-host sensitivity are real and must remain deployment/production-runner constraints. Do not add scheduler/load-admission complexity before the eventual runner is measured under its real behavior.

## Decision / integration status

Image-processing source correctness and performance are accepted for integration into `v2` under the limits above.

Integration is pending only because the exact validated `Cargo.lock` must first be committed byte-for-byte and a locked correctness check must pass on that resulting head.

The two current transient repository probe add/delete commits have net-zero tree effect and must not be interpreted as product changes.

M3 itself is **not complete** after this image slice. Remaining M3 includes at least:

- long-running worker loop / wakeup policy;
- lease renewal and cancellation during processing;
- video processing;
- duplicate detection;
- regeneration;
- progress/status UI;
- production-level background workload gate for the configured worker behavior.

## Deferred observations

- Full source SHA-256 verification costs one sequential source read before codec work; do not redesign without profile evidence.
- Standard-library canonicalization/symlink checks are not `openat2`/`O_NOFOLLOW` race-proof against a malicious local writer; media tree is service-controlled.
- Ingestion source orphans and processed staging orphans need eventual reconciliation/janitor work.
- Deployment must ensure API and worker share media storage with compatible UID/GID/permissions.
- Target host CPU is more constrained than RAM/NVMe; video will need its own explicit thread/concurrency benchmark.

## Local-agent evidence workflow

Execution-only target work remains read-only by default. Return one evidence ZIP with exact SHA/status, commands, raw stdout/stderr, environment/tool versions, measurements, invalid attempts, and cleanup proof. Preserve raw remote evidence until reviewed.

A local agent must not modify source/docs/config/SQL/commits/branches/deployments/persistent state without explicit user approval for that specific write.

## Single best next task

Obtain explicit user approval for one narrow local-agent write because this primary environment cannot safely copy the exact 40,521-byte lockfile through the GitHub text wrapper without risking byte transformation.

Approved write scope should be only:

1. fresh worktree/clone at the current `astra/m3-image-processing` head;
2. copy the exact `evidence/Cargo.lock` bytes from `m3-image-processing-load-curve-final-20261003T174724Z.zip` to `src/worker/v2/Cargo.lock`;
3. verify SHA-256 is exactly `4B355C9016EF56D71C78D4CFCC347BF0A6FD2F3DBA8E6F36465B6D3EDEDAA74A`;
4. commit only that file on `astra/m3-image-processing`;
5. run Rust 1.99 `cargo check --locked`, `cargo test --locked`, and `cargo clippy --locked --all-targets -- -D warnings`;
6. return one evidence ZIP and exact new commit SHA; do **not** update `v2` or `STATE.md` from the local agent.

After that evidence returns, the primary session should verify the lock commit/tree, update this state one final time, then fast-forward `v2` to the accepted feature head if no unrelated change has appeared.

Next implementation slice after integration: production worker loop with lease renewal/cancellation, preserving one-job concurrency and benchmarking the real continuous runner before adding any admission/scheduler complexity.
