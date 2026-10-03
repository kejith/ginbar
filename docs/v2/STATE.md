# Ginbar v2 State / Handoff

Last updated: 2026-10-03
Phase: M3 image-processing slice — FIRST TARGET GATE FAILED; CORRECTIVE PATCH READY FOR REVALIDATION
Integration branch: `v2`
Active feature branch: `astra/m3-image-processing`
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the resume point. Read it before `PLAN.md`. Do not rely on chat history as project memory.

## Branch status

- `master` remains untouched by rewrite work at `181fa44d79c7b4a1984c1a35795762dd503b3f77`.
- M1 board benchmark is complete.
- M2 fresh schema + core Go API is complete and integrated.
- M3 durable PostgreSQL media-job ownership/recovery boundary is complete and integrated.
- M3 upload/URL-ingestion + durable-enqueue boundary is complete and integrated.
- M3 Rust worker source-consumption/fenced-publication contract is complete, validated, and integrated.
- `v2` remains unchanged by the image-processing candidate at `e601c78486d219f97f98e55349209849f79c353f`.
- `astra/m3-image-processing` was branched from that exact `v2` SHA.
- First image-processing target gate tested `56ead29cc0f14fabf84469147faa65ecbe508a5f` and FAILED before tests/benchmarks because of formatting and Rust API compile errors.
- Corrective source/formatting commits are present through `89935e37fef6c10ed3cf3a0379dcb57eae74fc12`; this document is the state-only commit after that corrective head.
- The corrective head has not yet been compiled/tested/benchmarked. Do not infer a pass.
- Worker v2 lives under `src/worker/v2`; legacy `src/worker` remains reference-only unless explicitly reviewed for reuse.
- `.local-agent-results/` remains ignored for evidence ZIPs.

## Retained architecture and validated decisions

Keep unless new evidence contradicts them:

- Go 1.25 + pgx/v5 + standard `net/http` for API/workflows;
- PostgreSQL authoritative for application/workflow/durable job state;
- Rust worker for media processing;
- local NVMe for source/media storage;
- no Redis without measured need;
- immutable numeric relational IDs;
- at-least-once processing requires deterministic/idempotent durable side effects;
- initial production media-worker concurrency remains one job at a time;
- source verification and codec/filesystem work stay outside DB transactions;
- final DB publication is fenced by job ownership, lease generation, source identity, post eligibility, and post-lock real-time lease expiry.

The integrated durable-job boundary retains readiness-first `FOR UPDATE SKIP LOCKED` claiming, bounded retries, generation fencing, and no production polling loop yet.

## Integrated ingestion/enqueue boundary

Validated behavior remains:

- upload/URL bytes stage outside DB transactions;
- `media_sources` stores authoritative source key, byte size, and SHA-256;
- unreleased post + source + initial kind-0 job are created atomically;
- URL imports enforce SSRF restrictions;
- source keys use exact `sources/<2 lowercase hex>/<32 lowercase hex>` grammar;
- ambiguous DB commit preserves source bytes for reconciliation rather than risking a committed row pointing at missing data.

Accepted ingestion baselines:

- 8 MiB durable source staging: **45.1468 ms/op**, **186.06 MB/s**, ~67.6 KiB/op, 32 allocs/op;
- PostgreSQL ingestion CTE c1: **0.348 ms**, **2,870.87 TPS**, zero errors;
- PostgreSQL ingestion CTE c4: **0.457 ms**, **8,761.36 TPS**, zero errors.

## Integrated worker source/publication contract

`src/worker/v2/src/processing.rs` validates source-key grammar/path containment, rejects symlinks, enforces source byte limits, verifies filesystem size + full SHA-256, sniffs actual bytes, and rewinds the verified handle before processor dispatch. Filesystem/database I/O and cancellation are retryable; integrity/type/key/path failures are terminal.

Processed identity remains deterministic and versioned:

`media/<post shard>/<post id>/v<processing version>-<source SHA-256>.<format>`

`src/worker/v2/src/publication.rs` is the validated short DB commit point after durable output work. It fences exact running job ownership/generation, authoritative source SHA-256, post eligibility, and post-lock lease expiry; accepts an existing media row only when deterministic storage key and output digest agree; releases the post only after ready media exists; and succeeds/clears the job only after the whole publication statement succeeds.

### Accepted target-host baselines

Source open + verify + sniff + rewind, release mode, warm cache:

- 8 MiB: **35.104 ms**, **227.91 MiB/s**;
- 64 MiB: **298.757 ms**, **214.92 MiB/s**;
- 256 MiB: **1,122.598 ms**, **228.05 MiB/s**.

Corrected fenced publication SQL, PostgreSQL 17.11:

- 1,000/1,000 successful unique publications;
- **1.539 ms** average statement latency;
- **644.22 TPS**;
- bounded indexed plan, no unbounded sequential scan; one-row execution **0.949 ms**.

The prior source/publication-contract final evidence ZIP was `m3-worker-processing-contract-final-20261003T013900Z.zip`, SHA-256 `14DA7E1C4B8FB5495EFA6D2C1C920FD01CCF724F7441EFE5A928887216238ACB`, exact tested SHA `37ba916fad6e265f8e8ffde7b742533f8c55425a`; that contract PASSED and is integrated.

## M3 image-processing candidate

### Durable no-overwrite output publication

`src/worker/v2/src/output.rs` implements local-filesystem durable publication:

- output keys are relative under `media/` with normal path components;
- output directory components reject symlinks/non-directories;
- same-directory staging uses `create_new`;
- full staged bytes are written and file-`fsync`ed;
- final publication uses same-filesystem `hard_link`, which cannot replace an existing destination;
- containing directory is `fsync`ed after publication;
- staging file is removed and directory is `fsync`ed again;
- retries verify existing final byte size + SHA-256 before idempotent reuse;
- different bytes at the deterministic target are a terminal collision and are never overwritten.

Unit tests inject crashes after staged-file fsync and after final-directory fsync and cover exact reuse/collision preservation. Hidden staging orphans can remain after process death; correctness is safe, housekeeping remains future work.

### Bounded still-image processor

`src/worker/v2/src/image.rs` implements `BoundedImageProcessor` for:

- JPEG;
- non-animated PNG;
- non-animated WebP.

Animated PNG/WebP, GIF, AVIF/HEIF input, and video are terminal for this processing version rather than silently flattening/partially decoding them. Video remains out of this slice.

Candidate processing-version-1 constants/behavior:

- full decode before durable output publication;
- decoder orientation applied, dimensions revalidated afterward;
- input max dimension 16,384 px;
- max decoded pixels 80,000,000;
- decoder allocation hint 384 MiB;
- canonical image fits within 1,280 px without upscaling;
- center-crop view is resized directly to a 256x256 thumbnail without materializing the full crop;
- original full decode is dropped after main-image downsize;
- both AVIF objects encode before either is durably published;
- `ravif`: explicit one encoder thread, speed 10, main quality 75, thumbnail quality 60;
- `image` default features disabled; JPEG/PNG/WebP only;
- deterministic auxiliary `<canonical stem>.thumb.avif` published first, canonical deterministic `.avif` second;
- canonical AVIF SHA-256/size/dimensions feed the already-validated fenced DB publication.

Any output-affecting change after integration requires advancing `PROCESSING_VERSION`.

`src/worker/v2/src/main.rs` provides `process-once` only; it is not a production polling loop. It claims one job, verifies the source, performs bounded still-image processing, durably publishes outputs, then calls the fenced DB publication. Long-running lease renewal is not yet implemented; validation uses a comfortably long probe lease.

`src/worker/v2/tests/image_pipeline.rs` adds PostgreSQL-backed crash-before-DB retry/idempotency and deterministic canonical-key collision regressions. `examples/image_bench.rs` separates source verification from decode/transform/AVIF/durable-publication timing and uses fresh post IDs to prevent output reuse from faking codec performance.

## First image-processing target gate — FAIL

Evidence ZIP: `m3-image-processing-20261003T123812Z.zip`.
ZIP SHA-256: `1153CB218BC4654B0E57252236EBC638C87A02A7A124C3A7B51D6AA6E5EB9322`.
Exact tested SHA: `56ead29cc0f14fabf84469147faa65ecbe508a5f`.
Observed merge base: exact `v2` `e601c78486d219f97f98e55349209849f79c353f`.
Target: Ubuntu 24.04.3, Linux 6.8.0-88-generic, Intel i7-7700, 62 GiB RAM, ext4 `/dev/md2` RAID1 over two NVMe devices.

Important methodology deviation: the disposable build container used **Rust/Cargo 1.94.0**, not the requested 1.99.0. Therefore even a successful result from this run would not have satisfied the intended toolchain gate. The observed source/API failures are still actionable and were corrected; the next successful validation must use Rust 1.99.x.

Raw evidence showed:

- `cargo fmt --check`: FAIL; rustfmt changes were required in `examples/image_bench.rs`, `src/image.rs`, `src/output.rs`, `src/main.rs`, and `tests/image_pipeline.rs`;
- `cargo check`: FAIL with `E0277` because `SubImage<&DynamicImage>` itself did not satisfy `GenericImageView` at the direct resize call;
- `cargo check`: FAIL with `E0599` because `as_rgba` is supplied by `rgb::FromSlice`, while the candidate imported `ComponentSlice`;
- Clippy failed on the same compile errors and rejected the unused `ComponentSlice` import under `-D warnings`;
- no-DB `cargo test` failed at compilation, so no tests executed;
- dependency trees succeeded and resolved `image 0.25.10`, `ravif 0.13.0`, `rav1e 0.8.1`, `rgb 0.8.52`, `rayon 1.12.0`, `rayon-core 1.13.0`, `maybe-rayon 0.1.1`, root `sha2 0.10.9`, and transitive `sha2 0.11.0`;
- generated disposable `Cargo.lock` SHA-256: `4B355C9016EF56D71C78D4CFCC347BF0A6FD2F3DBA8E6F36465B6D3EDEDAA74A`;
- because the correctness gate failed, no release build, DB-enabled tests, codec/resource benchmarks, API load measurements, or process/thread measurements ran;
- server cleanup restored all pre-existing services/containers; raw remote evidence was retained by the executor for follow-up.

### Corrective patch after failed gate

No output semantics/constants were changed.

- `385ec92061920a8751f7931c9e9a161e65dbf3ae`: changed the crop resize to borrow the dereferenced `SubImageInner` (`&*view`), which is the `image 0.25.10` type implementing `GenericImageView`; changed the RGB slice trait import to `rgb::FromSlice`; applied rustfmt changes in `src/image.rs`.
- `098d1b9b58e94d48d5eb0dcfe9620f73eeccb888`: applied captured rustfmt changes to `examples/image_bench.rs`.
- `42d416afce7d3e70be3448767101022073fc9a6e`: applied captured rustfmt changes to `src/output.rs`.
- `b34ac39085da4f67157989b2d8447b1f03690bd6`: applied captured rustfmt changes to `src/main.rs`.
- `89935e37fef6c10ed3cf3a0379dcb57eae74fc12`: applied captured rustfmt changes to `tests/image_pipeline.rs`.

This primary session has no Rust toolchain, so those corrective commits are inspection-derived and **unvalidated**. Do not merge to `v2` until revalidation passes.

## Remaining observations

- The image/output work still exists only on the feature branch; integrated `v2` intentionally remains at the source/processor/fenced-publication boundary.
- The target evidence confirms ext4 on local mirrored NVMe, but output crash/idempotency tests did not execute because compilation failed.
- Full source SHA-256 verification still costs one sequential read before codec consumption; do not redesign without profile/codec evidence.
- Standard-library canonicalization + symlink checks are not equivalent to Linux `openat2`/`O_NOFOLLOW` against a malicious concurrent local filesystem writer; the media tree remains service-controlled.
- Ingestion source orphans and processed-output staging orphans need eventual janitor/reconciliation work before production enablement.
- `ravif`/`rav1e` pull Rayon/threading support transitively even though the candidate sets `with_num_threads(Some(1))`; runtime CPU/thread behavior must still be measured.
- The generated v2 `Cargo.lock` is currently evidence only. Because this worker is an application/binary, committing a lockfile is likely appropriate for reproducible production builds, but make that decision after the corrected Rust 1.99 correctness gate confirms the resolved graph.
- Deployment must ensure Go API and Rust worker share the media root with compatible UID/GID/permissions.

## Local-agent evidence workflow

For target-host work, use isolated/disposable resources and return one ZIP with exact SHA/status, commands, raw stdout/stderr, environment/tool versions, measurements, failures, and cleanup/restoration evidence. Preserve raw remote evidence until no longer needed.

## Single best next task

Run a **targeted corrective correctness gate first** against the exact current `astra/m3-image-processing` head using Rust/Cargo **1.99.x**: verify exact SHA/merge base, run `cargo fmt --check`, `cargo check`, `cargo clippy --all-targets -- -D warnings`, no-DB tests, dependency trees, and disposable-PostgreSQL full tests. Do not run codec/API benchmarks unless that correctness gate passes. If it passes, continue in the same evidence run with the previously specified real-media codec/resource benchmarks and matched API baseline-vs-one-worker latency/resource measurements, then return one raw evidence ZIP. Integrate into `v2` only after those results are reviewed here and accepted.
