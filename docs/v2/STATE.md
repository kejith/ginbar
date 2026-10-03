# Ginbar v2 State / Handoff

Last updated: 2026-10-03
Phase: M3 image-processing slice — SECOND CORRECTIVE GATE REVIEWED; FINAL CORRECTNESS / TARGET MEASUREMENT PENDING
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
- First target gate tested `56ead29cc0f14fabf84469147faa65ecbe508a5f` and failed on formatting plus two Rust API mismatches.
- Second corrective gate tested `6e1eba20155202841adb3717f224b2266d82627e` on Rust/Cargo 1.99.0. Formatting passed and the no-DB test command returned success, but the gate did not clear because one real Rust 1.99 Clippy lint remained and two requested Cargo commands were malformed by the validation harness/prompt.
- The demonstrated Rust 1.99 lint and the identical latent occurrence in the DB image test helper are corrected through `a718de413673e7650c188c659fc4627b26eb0d3c`; this document is the state-only commit after that corrective source head.
- The latest source corrections have not yet been revalidated. Do not merge to `v2` or claim codec/API performance yet.
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

`src/worker/v2/src/image.rs` implements `BoundedImageProcessor` for JPEG, non-animated PNG, and non-animated WebP. Animated PNG/WebP, GIF, AVIF/HEIF input, and video are terminal for this processing version rather than silently flattening/partially decoding them. Video remains out of this slice.

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

The disposable build container used Rust/Cargo 1.94.0 instead of the requested 1.99.0, so it could never satisfy the intended toolchain gate. It still exposed actionable source defects:

- rustfmt differences in five candidate files;
- `E0277`: direct `SubImage<&DynamicImage>` resize type mismatch;
- `E0599`: `as_rgba` requires `rgb::FromSlice`, not `ComponentSlice`;
- no tests/benchmarks ran because compilation failed.

Those failures were corrected without changing output semantics/constants. The target filesystem was confirmed as ext4 `/dev/md2` RAID1 over two NVMe devices.

## Second corrective target gate — PARTIAL / NOT A PASS

Evidence ZIP: `m3-image-processing-corrective-20261003T130603Z.zip`.
ZIP SHA-256: `C7CBC73C08E6E2F7E44C3FD94B2F91B2FBE0C4AE96EB42C77E6E3E1B2A484652`.
Exact tested SHA: `6e1eba20155202841adb3717f224b2266d82627e`.
Expected/observed merge base: `v2` `e601c78486d219f97f98e55349209849f79c353f`.
Toolchain: `rustc 1.99.0`, `cargo 1.99.0`, `rustfmt 1.10.0-stable`, `cargo clippy 0.1.99`, NASM 2.16.01.
Target: Ubuntu 24.04.3, Linux 6.8.0-88-generic, Intel i7-7700, ext4 `/dev/md2`.

Raw evidence establishes:

- `cargo fmt --check`: **PASS**;
- requested `cargo check --locked=false ...`: harness/prompt error, not a product result — Cargo 1.99 rejects a value for boolean `--locked`, so the crate was not checked by that command;
- `cargo clippy --all-targets -- -D warnings`: **FAIL** on one demonstrated product lint, `clippy::chunks-exact-to-as-chunks`, at the unit-test PNG helper in `src/image.rs`;
- normal `cargo test`: command exit 0, with 34 invoked tests returning success and zero ordinary Rust test failures;
- that no-DB run is **not** evidence that PostgreSQL paths executed: DB-backed tests intentionally return early when `GINBAR_TEST_DATABASE_URL` is absent, and Cargo captures successful-test stderr, so absence of visible skip text does not prove execution;
- normal `cargo tree`: **PASS**;
- requested `cargo tree ... -e features`: harness line-ending error, not a product result — the captured command file ended with CRLF and Cargo received `features\r`;
- generated disposable lockfile SHA-256 again matched the first gate exactly: `4B355C9016EF56D71C78D4CFCC347BF0A6FD2F3DBA8E6F36465B6D3EDEDAA74A`;
- resolved graph again included `image 0.25.10`, `ravif 0.13.0`, `rav1e 0.8.1`, `rgb 0.8.52`, `rayon 1.12.0`, `rayon-core 1.13.0`, `maybe-rayon 0.1.1`, root `sha2 0.10.9`, and transitive `sha2 0.11.0`;
- because the gate did not clear, no disposable PostgreSQL stage, release build, codec/resource benchmark, worker runtime/thread measurement, or API interference measurement ran;
- cleanup removed all run-owned resources and preserved pre-existing services/containers and production state.

### Corrections after second gate

No output-affecting semantics/constants changed.

- `a55ece38f775429e3055237a15beb80c98e0ba94`: unit-test PNG helper changed from `chunks_exact_mut(4)` to Rust 1.99's `as_chunks_mut::<4>()` form required by Clippy `-D warnings`.
- `a718de413673e7650c188c659fc4627b26eb0d3c`: the DB image-pipeline PNG helper contained the identical pattern; it was changed proactively to `as_chunks_mut::<4>()` so the next `--all-targets` run does not simply fail on the second occurrence after the first is fixed.

The repeated lockfile hash across independent Rust 1.94 and Rust 1.99 target runs is strong evidence that the current resolved graph is stable. Because this worker is a production application/binary and AVIF output is codec-version-sensitive, a committed `Cargo.lock` remains the intended production choice before integration. It is not yet committed on this branch; the next exact-SHA gate must record the generated lock hash and stop if it differs from the accepted evidence hash above.

## Remaining observations

- The image/output work still exists only on the feature branch; integrated `v2` intentionally remains at the source/processor/fenced-publication boundary.
- Full source SHA-256 verification still costs one sequential read before codec consumption; do not redesign without profile/codec evidence.
- Standard-library canonicalization + symlink checks are not equivalent to Linux `openat2`/`O_NOFOLLOW` against a malicious concurrent local filesystem writer; the media tree remains service-controlled.
- Ingestion source orphans and processed-output staging orphans need eventual janitor/reconciliation work before production enablement.
- `ravif`/`rav1e` pull Rayon/threading support transitively even though the candidate sets `with_num_threads(Some(1))`; runtime CPU/thread behavior must still be measured.
- The one-shot worker does not renew its lease during long codec work; production polling/renewal remains a later M3 concern after this processing path is measured.
- Deployment must ensure Go API and Rust worker share the media root with compatible UID/GID/permissions.

## Local-agent evidence workflow

For target-host work, use isolated/disposable resources and return one ZIP with exact SHA/status, commands, raw stdout/stderr, environment/tool versions, measurements, failures, and cleanup/restoration evidence. Preserve raw remote evidence until no longer needed.

## Single best next task

Run one final exact-SHA target-host gate on Rust/Cargo 1.99.x against the current `astra/m3-image-processing` head. Use Linux-native commands without CRLF command-file interpolation:

1. `cargo fmt --manifest-path src/worker/v2/Cargo.toml -- --check`
2. `cargo check --manifest-path src/worker/v2/Cargo.toml`
3. `cargo clippy --manifest-path src/worker/v2/Cargo.toml --all-targets -- -D warnings`
4. no-DB `cargo test --manifest-path src/worker/v2/Cargo.toml`
5. `cargo tree --manifest-path src/worker/v2/Cargo.toml`
6. `cargo tree --manifest-path src/worker/v2/Cargo.toml --edges features`
7. verify generated `Cargo.lock` SHA-256 is `4B355C9016EF56D71C78D4CFCC347BF0A6FD2F3DBA8E6F36465B6D3EDEDAA74A` before continuing;
8. if all above pass, run the full suite with a disposable PostgreSQL 17 and prove the DB-backed image tests actually execute;
9. only then build release artifacts and run real JPEG/PNG/WebP codec measurements, the large-valid-image RSS/CPU probe, runtime thread/concurrency observation, and matched API baseline versus one concurrent worker job;
10. return one raw evidence ZIP for review. Integrate into `v2` only after that evidence is accepted.
