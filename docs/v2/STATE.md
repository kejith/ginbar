# Ginbar v2 State / Handoff

Last updated: 2026-10-03
Phase: M3 image-processing slice — CORRECTNESS/PERFORMANCE ACCEPTED; EXACT LOCK COMMIT PREPARED LOCALLY; REBASE/LOCKED VALIDATION + PUSH PENDING
Integration branch: `v2`
Active feature branch: `astra/m3-image-processing`
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the resume point. Read it before `PLAN.md`. Do not rely on chat history as project memory.

## Branch safety / status

- `master` remains legacy/read-only at `181fa44d79c7b4a1984c1a35795762dd503b3f77`.
- `v2` remains unchanged by this image slice at `e601c78486d219f97f98e55349209849f79c353f`.
- The image-processing implementation was branched from exact `v2` SHA `e601c78486d219f97f98e55349209849f79c353f`.
- Accepted image-processing source semantics remain commit `e53605838075509800efeaa644319d29e18587f2`.
- Commits after `e536058...` on the feature branch are state/history-only or transient net-zero repository probes; no image-processing source semantics changed.
- Primary-session state updates have advanced remote `astra/m3-image-processing` beyond `e9ae4c0380481d8dfc7819b43b26b19990658d93` without changing product source. Fetch the exact current remote feature head before any local-agent push.
- Do not modify `master`.
- Do not fast-forward `v2` until the exact validated `Cargo.lock` commit is on the remote feature branch and locked correctness passes on that exact resulting head.
- Worker v2 is under `src/worker/v2`; legacy `src/worker` is reference-only.
- `.local-agent-results/` remains ignored for local evidence ZIPs; a local checkout may use `.git/info/exclude` rather than a tracked ignore change.

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

Expected Git blob SHA-1 for those exact bytes:

`9bd2933d1a25f84cb04dc354476a3e6c7cdb0619`

The corrected load-curve run reconstructed this graph from crates.io with:

```text
cargo generate-lockfile
cargo update -p mio --precise 1.2.3
```

and then used `--locked` for check/test/Clippy/build.

A prior unconstrained fresh resolution changed only `mio 1.2.3 -> 1.2.4`, producing lock SHA `00E6C634CCFFBAC6A25DC0CDDBC00A2A58E3FE05AB4AF639412BDAFE9AE362DB`; that was registry-resolution drift, not source drift. This confirms an application lockfile is required.

The exact validated lock bytes are preserved in `m3-image-processing-load-curve-final-20261003T174724Z.zip` as `evidence/Cargo.lock` and `evidence/Cargo.lock.reconstructed`.

### Local-agent lock commit status

User explicitly approved the narrow lockfile write.

The local agent verified the source evidence ZIP SHA-256 `C2432D3B4E38A8ADFC842DE2205903BEE0C31B9A3EC1D26F9AD4DF13CF80DD13`, binary-extracted `evidence/Cargo.lock`, and independently verified:

- size: **40,521 bytes**;
- SHA-256: `4B355C9016EF56D71C78D4CFCC347BF0A6FD2F3DBA8E6F36465B6D3EDEDAA74A`;
- Git blob SHA-1: `9bd2933d1a25f84cb04dc354476a3e6c7cdb0619`.

It copied only those bytes into an isolated clone at parent `e9ae4c0380481d8dfc7819b43b26b19990658d93` and created local commit:

`f684fbd747ddd2c3295ccbebe4f12e85a92ba0a1`

That local commit contains exactly one path, `src/worker/v2/Cargo.lock`, with the required hashes. The isolated checkout was clean afterward.

However, the agent stopped before the required `cargo check --locked`, `cargo test --locked`, and `cargo clippy --locked --all-targets -- -D warnings` validation and did **not** push the commit. Primary-session state commits subsequently advanced the remote feature branch, so `f684fbd...` can no longer be pushed directly as a fast-forward.

Preferred continuation is to preserve the exact lock commit content, fetch the current remote feature head, rebase/cherry-pick the one-file lock change onto that current head without changing its bytes, run the locked gate on the rebased head, then push normally if everything passes. Do not regenerate the lockfile.

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

Integration is pending only on completing the exact lock commit workflow:

1. fetch current remote `astra/m3-image-processing` head; it will be state-only descendants of `e9ae4c...`;
2. preserve the exact one-file lock change from local commit `f684fbd747ddd2c3295ccbebe4f12e85a92ba0a1` while rebasing/cherry-picking it onto the current remote feature head;
3. run Rust 1.99 `cargo check --locked`, `cargo test --locked`, and `cargo clippy --locked --all-targets -- -D warnings` on that rebased head;
4. re-verify lock size/SHA/Git blob and one-file lock-change scope after those commands;
5. verify `v2` and `master` are unchanged;
6. push the rebased feature head normally without force;
7. return evidence to the primary session;
8. primary session verifies remote tree, updates this state one final time, then fast-forwards `v2` if no unrelated change appeared.

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

Continue the already-approved lockfile operation; do **not** regenerate the lock or alter any product source.

Required continuation:

1. preserve/verify local commit `f684fbd747ddd2c3295ccbebe4f12e85a92ba0a1` and its exact lock blob `9bd2933d1a25f84cb04dc354476a3e6c7cdb0619`;
2. fetch the exact current remote `astra/m3-image-processing` head and verify all remote changes since `e9ae4c...` are docs/state-only;
3. rebase or cherry-pick only the one-file lock change onto that current remote head, producing one new local feature head without touching `v2` or `master`;
4. verify the resulting lock size is 40,521 bytes, SHA-256 is `4B355C...AA74A`, Git blob is `9bd293...0619`, and no other path is changed by the lock commit;
5. use Rust/Cargo 1.99.x and run `cargo check --locked`, `cargo test --locked`, and `cargo clippy --locked --all-targets -- -D warnings` on the rebased head;
6. re-fetch remote refs and require the remote feature has not moved since step 2, while `v2` remains `e601c784...` and `master` remains `181fa44d...`;
7. push the rebased feature head normally, without force;
8. return one evidence ZIP with old/new commit identities, rebase/cherry-pick evidence, locked logs, exact hashes, remote refs, and final clean status; do not update `STATE.md` or `v2` from the local agent.

After that evidence returns, verify and fast-forward the image slice into `v2`. Next implementation slice: production worker loop with lease renewal/cancellation, preserving one-job concurrency and benchmarking the real continuous runner before adding any admission/scheduler complexity.