# Ginbar v2 State / Handoff

Last updated: 2026-10-03
Phase: M3 image-processing slice IMPLEMENTED ON FEATURE BRANCH — VALIDATION / TARGET MEASUREMENT PENDING
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
- M3 Rust worker source-consumption/publication contract is complete, validated, and integrated.
- `v2` remains unchanged by the image-processing candidate at `e601c78486d219f97f98e55349209849f79c353f`.
- `astra/m3-image-processing` was branched from that exact `v2` SHA. Image-processing implementation and README changes are present through `d057e6f5bc9950c46c425d3fcbec01642c12a4a0`; this state update is a documentation-only commit after that candidate SHA.
- The image-processing candidate has **not** been compiled, tested, benchmarked, or integrated from this session because the available execution environment lacks the required Rust/target-host runtime. Do not infer a pass or performance result from code inspection.
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
- final publication is fenced by job ownership, lease generation, source identity, post eligibility, and post-lock real-time lease expiry.

The integrated durable-job boundary retains readiness-first `FOR UPDATE SKIP LOCKED` claiming, bounded retries, generation fencing, and no production polling loop yet.

## Integrated ingestion/enqueue boundary

The prior ingestion slice remains validated:

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

## Integrated worker source-consumption / publication contract

### Source preparation

`src/worker/v2/src/processing.rs` provides the validated kind-0 source boundary:

- load authoritative `media_sources` by immutable post ID;
- exact source-key grammar validation;
- canonical shared-root containment checks;
- reject symlink source files and shard directories;
- enforce configured maximum source bytes;
- compare filesystem and DB byte sizes;
- stream with a fixed 128 KiB buffer while checking cancellation;
- verify full SHA-256 against authoritative source metadata;
- sniff actual bytes instead of trusting declared MIME;
- rewind and pass the same verified file handle to processor dispatch.

Recognized routing families are JPEG, PNG, GIF, WebP, AVIF/HEIF-family ISO-BMFF images, MP4-family video, and EBML-family video. This is family routing only; concrete codecs still own full parse/decode validation.

Error classification is explicit: database/filesystem I/O and cancellation are retryable; missing source, unsupported job kind, invalid digest/key/path, integrity mismatch, and unsupported media type are terminal.

### Processor boundary and deterministic identity

`ImageProcessor` and `VideoProcessor` are explicit traits. The integrated `v2` branch still contains only the validated boundary; the first concrete `ImageProcessor` exists only on the unvalidated feature branch described below.

Processed output identity is deterministic and versioned:

`media/<post shard>/<post id>/v<processing version>-<source SHA-256>.<format>`

Identity binds immutable post ID, verified source digest, processing-contract version, and output format. At-least-once retries therefore have a stable side-effect target. A processing algorithm that changes output semantics must advance the processing version instead of silently reusing incompatible keys.

### Fenced processed-media publication

`src/worker/v2/src/publication.rs` is the short authoritative commit point after durable output-file work.

The validated single PostgreSQL statement:

1. identifies the exact running kind-0 job by job ID, post ID, worker ID and lease generation;
2. requires current authoritative `media_sources.sha256` to equal the verified source digest;
3. requires the post to be undeleted and in releasable state `(0,1)` **before** any media write;
4. `FOR UPDATE`-locks both job and post;
5. rechecks lease expiry with `clock_timestamp()` after lock acquisition;
6. inserts ready `media`, or accepts an existing row only when deterministic storage key and output SHA-256 match;
7. releases the post only after a ready media row is accepted;
8. succeeds the job and clears ownership only after media + release succeed.

Rust-side validation additionally requires the deterministic output key to embed the same verified source digest.

Rejected stale/expired/source-changed/deleted/non-releasable/conflicting cases leave no unintended ready media, do not release the post, and do not succeed the job.

## Worker processing validation history

### First full gate — FAIL

Evidence ZIP: `m3-worker-processing-contract-20261003T011708Z.zip`.
ZIP SHA-256: `205DF7A3F16E2964B9779E0D36DCCE0E8DB4B199BF5A4C8C62A601FEADA1EB76`.
Exact tested SHA: `4a9a5e1a4296871945146ea15dcfce6b419169ba`.

The full gate passed source/path/integrity behavior, full PostgreSQL tests, fencing, query-plan shape, lock-wait expiry, cleanup, and target measurements. It failed on three items:

- `cargo fmt --check` formatting differences;
- Rust 1.99 Clippy `chunks_exact_to_as_chunks` denial;
- a real SQL ordering defect where a soft-deleted post could receive ready `media` before the later release update rejected it.

Corrective commits applied formatting, used `as_chunks::<4>()`, moved post eligibility/locking ahead of media insertion, and added committed deleted/non-releasable regressions.

### Final targeted gate — PASS

Evidence ZIP: `m3-worker-processing-contract-final-20261003T013900Z.zip`.
ZIP SHA-256: `14DA7E1C4B8FB5495EFA6D2C1C920FD01CCF724F7441EFE5A928887216238ACB`.
Exact tested SHA: `37ba916fad6e265f8e8ffde7b742533f8c55425a`.
Environment: Ubuntu 24.04.3, Linux 6.8.0-88-generic, Intel i7-7700, Rust 1.99.0, Cargo 1.99.0, PostgreSQL 17.11, ext4 on local mirrored NVMe. Resolved production `sha2`: **0.10.9**.

Verified from raw evidence:

- exact feature SHA, exact `v2` merge base, expected four-file corrective delta, and unchanged `master`;
- `cargo fmt --check`, `cargo check`, and `cargo clippy --all-targets -- -D warnings` passed;
- no-DB run: 8 unit tests passed; only DB-backed tests emitted the intentional unset-URL skip markers;
- PostgreSQL run: 8 unit + 5 durable-job + 5 processing-contract tests passed; zero DB-enabled skips;
- deleted/non-releasable publication regression: **10/10**, zero failures/flakes/skips/timeouts;
- publication lock-wait expiry regression: **10/10**, zero failures/flakes/skips/timeouts;
- predecessor durable-job post-lock expiry regression passed;
- focused wrong-owner, stale-generation, source-digest-change, already-expired, existing-media-conflict, deleted-post, and success probes all executed and passed;
- success path produced exactly one ready media row, released the post, succeeded/cleared the job, and confirmed source verification occurred outside a DB transaction;
- corrected query plan used bounded indexed access and locked job/post eligibility before `written_media`; no unbounded sequential scan appeared;
- cleanup removed the disposable database/container/network/checkouts/build outputs while leaving repository refs and production state untouched.

Some harness/setup attempts were corrected during the gate (missing Rust components in the first disposable container, PostgreSQL readiness timing, malformed `cargo tree` package syntax, and an initial zero-test benchmark filter). Final evidence files are from the corrected executions; none of those attempts was counted as a product pass.

Decision: the worker source-consumption/publication contract PASSES and is integrated into `v2`.

## Accepted target-host worker baselines

### Source open + verify + sniff + rewind

Release mode, warm page cache, 128 KiB verification buffer, 15 operations per size:

- 8 MiB: **35.104 ms**, **227.91 MiB/s**;
- 64 MiB: **298.757 ms**, **214.92 MiB/s**;
- 256 MiB: **1,122.598 ms**, **228.05 MiB/s**.

These remain the baseline until source verification architecture materially changes. They measure source preparation only, not decode/encode.

### Corrected publication SQL

PostgreSQL 17.11, concurrency 1, 1,000 unique successful publications:

- **1,000/1,000**, zero errors;
- **1.539 ms average statement latency**;
- **644.22 TPS**;
- final state: 1,000 ready media rows, 1,000 released posts, 1,000 succeeded jobs.

The corrected one-row `EXPLAIN (ANALYZE, BUFFERS)` used bounded index paths for job/source/post/media, with no unbounded sequential scan; execution time was **0.949 ms**. Top-level buffers: shared hit=58, read=4, dirtied=11, written=6.

The pre-fix 1.419 ms / 698.34 TPS measurement is historical only and must not be used as the accepted publication baseline.

## M3 image-processing candidate — unvalidated

Feature branch: `astra/m3-image-processing`, based exactly on `v2` `e601c78486d219f97f98e55349209849f79c353f`.
Implementation/README candidate before this state-only commit: `d057e6f5bc9950c46c425d3fcbec01642c12a4a0`.

### Durable output publication

New `src/worker/v2/src/output.rs` implements a local-filesystem no-overwrite publication protocol:

- output keys must be relative under `media/` with normal path components;
- output root and directory components reject symlinks/non-directories;
- same-directory staging uses `create_new`;
- complete staged bytes are written and file-`fsync`ed;
- final publication uses same-filesystem `hard_link`, which fails rather than replacing an existing destination;
- the containing directory is `fsync`ed after final publication;
- the staging name is removed and the directory is `fsync`ed again;
- retries encountering an existing final stream and verify exact byte size + SHA-256 before accepting it as idempotent reuse;
- different bytes at the deterministic target are a terminal collision and are never overwritten.

Unit tests inject crashes after staging-file fsync and after final directory fsync, and cover exact reuse plus collision preservation. A process crash can leave a hidden staging orphan; this is safe for correctness but needs later janitor/housekeeping.

This protocol assumes the deployment media root is a local filesystem with normal same-directory hard-link/fsync durability semantics. The target ext4/NVMe host must validate that assumption.

### Bounded still-image processor

New `src/worker/v2/src/image.rs` implements `BoundedImageProcessor`.

Current supported still input is deliberately narrow:

- JPEG;
- non-animated PNG;
- non-animated WebP.

Animated PNG/WebP, GIF, AVIF/HEIF input, and video are terminal in this processing version rather than silently flattening or partially decoding them. Video remains out of this slice.

The candidate:

- fully decodes before any durable output publication;
- reads/applies decoder orientation and revalidates dimensions afterward;
- caps input dimension at 16,384 px, decoded pixels at 80,000,000, and decoder allocation hint at 384 MiB;
- downsizes the canonical image to fit 1,280 px without upscaling;
- center-crops by view and directly resizes the thumbnail to 256x256 without materializing a full-size crop buffer;
- explicitly drops the original full decode after a main-image downsize to reduce peak retained memory;
- encodes both outputs before publishing either file;
- uses `ravif` with explicit one-thread encoding, speed 10, main quality 75, thumbnail quality 60;
- builds `image` without default features and enables only JPEG/PNG/WebP decoding;
- publishes a deterministic auxiliary `<canonical stem>.thumb.avif` first and the integrated canonical `processed_output_key(...).avif` second;
- returns canonical AVIF SHA-256/size/dimensions to the already-validated fenced DB publication path.

Those codec limits/quality/speed settings are **candidate processing-version-1 constants**, not accepted production settings yet. Any output-affecting change after integration requires advancing `PROCESSING_VERSION`.

### Execution path and tests

`src/worker/v2/src/main.rs` now has a `process-once` probe that claims, verifies, processes, durably publishes, and then executes the existing fenced DB publication for at most one still-image job. It is not a production polling loop. `GINBAR_MEDIA_ROOT` is required; source size defaults to 512 MiB; target probes should use a lease comfortably above measured one-image duration because periodic renewal during codec work is not implemented in this probe.

New PostgreSQL-backed `tests/image_pipeline.rs` covers:

- crash after both durable output files but before DB publication, followed by lease expiry/reclaim, deterministic file reuse, and successful fenced retry;
- deterministic canonical-key collision preserving unrelated bytes and preventing DB media publication/post release.

New `examples/image_bench.rs` times verified-source preparation separately from decode/transform/AVIF/durable-publication and uses a fresh post ID each iteration so output reuse cannot fake codec performance.

### Validation status and unresolved gates

No local or target execution result exists yet for this candidate. Required before integration:

- exact-SHA `cargo fmt --check`, `cargo check`, and `cargo clippy --all-targets -- -D warnings` on Rust 1.99;
- no-DB and disposable-PostgreSQL full test runs, including all predecessor worker tests and new image tests;
- confirm `image` / `ravif` / `rav1e` / `rgb` resolved versions and inspect any generated `Cargo.lock`; the v2 worker still has no committed lock, so transitive build/output reproducibility must be decided before production integration;
- real supported-media codec benchmarks on the target host, with raw per-image verify/process timings, output sizes, CPU, RSS, and environment/tool/dependency versions;
- a large-but-valid synthetic input resource probe to test the candidate memory/pixel limits;
- matched same-run API latency/throughput measurements with and without one concurrent worker image job, using the existing v2 HTTP benchmark harness and disposable database/media root; report p50/p95/p99/max/RPS/errors and resource samples rather than claiming a speedup from design alone;
- verify the worker remains one job at a time and AVIF encoder thread count remains bounded during the load measurement;
- verify cleanup leaves production/Wallium and repository refs/state untouched.

Potential compile issue to settle from evidence rather than guesswork: the candidate uses `rgb::ComponentSlice` because `ravif` 0.13 documents that conversion path; if Rust 1.99 Clippy/deprecation warnings reject it under `-D warnings`, change it only after capturing the exact failure.

## Remaining observations

- The durable output/codec work above exists only on the unvalidated feature branch; integrated `v2` still intentionally stops at the source/processor/publication contract.
- Standard-library canonicalization + symlink checks are not equivalent to Linux `openat2`/`O_NOFOLLOW` race-proof resolution against a malicious concurrent local filesystem writer. The current media tree is service-controlled; revisit only if that threat model changes.
- Full source SHA-256 verification costs one sequential read before codec consumption. Do not redesign it without real codec/profile evidence.
- A process crash between ingestion source publication and authoritative DB commit can leave an unreferenced source file. Orphan reconciliation/janitor remains required before production ingestion is enabled.
- Processed-output staging orphans are similarly possible after process death and need eventual housekeeping, though deterministic final publication remains correct.
- The one-shot image command does not renew its lease during long codec work; the target gate must use a sufficiently long probe lease. A production runner will need a measured renewal/cancellation strategy before long-running workloads are enabled.
- Deployment must ensure Go API and Rust worker share the media root with compatible UID/GID/permissions.

## Local-agent evidence workflow

For target-host work, use isolated/disposable resources and return one ZIP with exact SHA/status, commands, raw stdout/stderr, environment/tool versions, measurements, failures, and cleanup/restoration evidence. Preserve raw remote evidence until no longer needed.

## Single best next task

Run the complete read-only/execution-only target-host validation gate against the exact `astra/m3-image-processing` state commit: compile/lint/test the candidate and predecessor contracts, run real image codec/resource benchmarks, and run matched API baseline versus one concurrent worker image job on disposable state. Return one raw evidence ZIP. Analyze that evidence here, fix/tune the feature branch if needed, then integrate into `v2` only after the gate passes and the API-latency/resource impact is acceptable.
