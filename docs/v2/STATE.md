# Ginbar v2 State / Handoff

Last updated: 2026-10-03
Phase: M3 media pipeline integration — worker source-consumption/publication contract FIXED AFTER FAILED GATE; targeted revalidation pending
Integration branch: `v2`
Active M3 branch: `astra/m3-worker-processing-contract`
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the resume point. Read it before `PLAN.md`. Do not rely on chat history as project memory.

## Branch status

- `master` remains read-only for rewrite work at `181fa44d79c7b4a1984c1a35795762dd503b3f77`.
- `v2` remains at `a552e96954ec2de42523bb5449a52eff0b216ec8`.
- M1 board benchmark is complete.
- M2 fresh schema + core Go API is complete and integrated.
- M3 durable PostgreSQL media-job ownership/recovery boundary is complete and integrated.
- M3 upload/URL-ingestion + durable-enqueue boundary is complete and integrated.
- The worker processing-contract slice remains isolated on `astra/m3-worker-processing-contract`, based on exact `v2` tip `a552e96954ec2de42523bb5449a52eff0b216ec8`.
- The first full worker-processing gate tested exact SHA `4a9a5e1a4296871945146ea15dcfce6b419169ba` and failed on formatting, one Clippy lint, and one real publication invariant defect. All other requested correctness probes, full PostgreSQL tests, query-plan checks, and measurements passed.
- Corrective commits after that gate:
  - `0ffebe2b47cb64dccba6995b4d1931b520dc9931` — apply the recorded Rust formatting changes and replace the denied `chunks_exact_to_as_chunks` pattern;
  - `b027d05624ec1af04c7d3a5c2009bbece1217b11` — prevent ready-media insertion unless the post is currently releasable, locking both job and post before the write;
  - `2860b5975032bb653789cfbba8bcc02d4f1f6a82` — add committed regression coverage for soft-deleted and non-releasable posts.
- This slice changes only `src/worker/v2/**` plus this handoff document.
- `.local-agent-results/` remains ignored for evidence ZIPs.

## Retained architecture and validated decisions

Keep unless new evidence contradicts them:

- Go 1.25 + pgx/v5 + standard `net/http` for API/workflows;
- PostgreSQL authoritative for app/workflow/durable job state;
- Rust worker for media processing;
- local NVMe for source/media storage;
- no Redis without measured need;
- immutable numeric relational IDs;
- at-least-once processing requires deterministic/idempotent durable side effects;
- initial production media-worker concurrency target remains one job at a time.

The integrated media-job boundary retains readiness-first `FOR UPDATE SKIP LOCKED` claiming, lease-generation fencing, post-lock `clock_timestamp()` expiry checks, bounded retries, and no production polling loop yet.

## Integrated ingestion/enqueue boundary

The prior M3 slice is integrated and validated:

- upload/URL bytes stage outside DB transactions;
- `media_sources` records authoritative source storage key, byte size, and SHA-256;
- unreleased post + source + initial kind-0 job are created atomically;
- URL fetches enforce SSRF restrictions;
- source keys use exact `sources/<2 lowercase hex>/<32 lowercase hex>` grammar;
- ambiguous DB commit preserves source bytes rather than risking a committed row pointing at missing data.

Accepted target baselines from that slice:

- 8 MiB source staging durability path: mean **45.1468 ms/op**, **186.06 MB/s**, ~67.6 KiB/op, 32 allocs/op;
- PostgreSQL ingestion CTE c1: **0.348 ms**, **2,870.87 TPS**, zero errors;
- PostgreSQL ingestion CTE c4: **0.457 ms**, **8,761.36 TPS**, zero errors.

## M3 worker source-consumption / publication contract

### Scope

This slice connects claimed kind-0 jobs to a verified source/processor/publication contract. It deliberately does **not** implement real AVIF/image encoding, ffmpeg/video processing, perceptual duplicate policy, regeneration, a production polling loop, or progress UI.

No Go/API/frontend code changes in this slice.

### Source loading and verification

`src/worker/v2/src/processing.rs` provides:

- authoritative `media_sources` lookup by immutable `post_id`;
- `prepare_claimed_source` for claimed kind-0 jobs;
- exact ingestion-key grammar validation;
- shared-root canonical containment checks;
- rejection of symlink source files and shard directories;
- caller-supplied maximum source-byte bound;
- filesystem-size vs DB-size verification;
- fixed 128 KiB streaming buffer;
- SHA-256 verification with `sha2 = "0.10"`;
- cancellation checks during full verification read;
- actual media-family sniffing instead of trusting declared MIME;
- rewind of the same verified file handle before processor dispatch.

Recognized routing families are JPEG, PNG, GIF, WebP, AVIF/HEIF-family ISO-BMFF images, MP4-family video, and EBML-family video. This is dispatch classification, not full decoder validation.

Error classification remains explicit: PostgreSQL/filesystem I/O and cancellation are retryable; missing source, unsupported job kind, invalid digest/key/path, size/hash mismatch, and unsupported media type are terminal.

### Processor boundaries

`ImageProcessor` and `VideoProcessor` receive a verified, rewound source handle. There are no concrete codec implementations yet.

The verified handle is reused rather than reopened before dispatch. Codec consumption will still read the source after the verification pass; retain measurement evidence before considering a combined verify/decode architecture.

### Deterministic processed-output identity

`processed_output_key` defines:

`media/<post shard>/<post id>/v<processing version>-<source SHA-256>.<format>`

Identity includes immutable post ID, authoritative source digest, processing-contract version, and output format. Future output-file publication must be no-overwrite/idempotent under this identity; a retry may reuse an existing deterministic object only after proving it is the same result.

Concrete output-file staging/fsync/collision handling remains intentionally deferred to the first codec slice.

### Fenced processed-media publication

`src/worker/v2/src/publication.rs` is the short authoritative DB commit point after durable output-file work.

The corrected single PostgreSQL statement now:

1. joins the exact running kind-0 job to its authoritative source **and post**;
2. requires job ID, post ID, worker ID, lease generation, and source SHA-256 to match;
3. requires `post.deleted_at IS NULL` and `post.release_state IN (0, 1)` before any media write;
4. `FOR UPDATE`-locks both the job and post rows;
5. rechecks lease expiry against `clock_timestamp()` after lock acquisition;
6. inserts ready `media` (`processing_state = 1`) or accepts an existing row only when deterministic storage key and output SHA-256 are identical;
7. releases the post only after a ready media row is accepted;
8. succeeds the fenced job and clears ownership only after media + release succeed.

Rust-side validation also requires the deterministic output key to embed the verified source digest.

An expired/stale lease, source-identity change, deleted/non-releasable post, or conflicting prior media identity must therefore yield `LeaseLostOrConflict` with no new ready media, no release, and no job success. No DB transaction spans source hashing or future codec/filesystem work.

## First full worker-processing gate — FAIL

Evidence ZIP: `m3-worker-processing-contract-20261003T011708Z.zip`.
ZIP SHA-256: `205DF7A3F16E2964B9779E0D36DCCE0E8DB4B199BF5A4C8C62A601FEADA1EB76`.
Exact tested SHA: `4a9a5e1a4296871945146ea15dcfce6b419169ba`.
Environment: Ubuntu 24.04.3, Linux 6.8.0-88-generic, Intel i7-7700, Rust 1.99.0, Cargo 1.99.0, PostgreSQL 17.11, ext4 `/dev/md2` mirrored Samsung NVMe. Resolved worker `sha2`: **0.10.9**.

Passed:

- exact SHA/base/clean detached-checkout provenance; master unchanged;
- `cargo check`;
- no-DB unit tests: 8 passed; expected DB tests skipped only because the DB URL was unset;
- full PostgreSQL suite: 8 unit + 5 durable job-state + 4 processing-contract tests, all passed and none skipped;
- predecessor durable-job lock-wait expiry regression;
- successful publication state semantics and proof source verification was outside a DB transaction;
- source-identity, wrong-owner, stale-generation, and expired-lease fencing;
- publication row-lock wait across expiry: 10/10 passes, zero flakes/timeouts/skips;
- existing-media conflict preservation;
- source path/symlink/integrity/security probes;
- byte-sniffing independence from declared MIME;
- retryable/terminal error classification;
- bounded publication query plan with no sequential scans;
- cleanup/restoration with no source/ref/production-state writes.

Failures:

1. `cargo fmt --check` returned exit 1 with deterministic formatting diffs in `processing.rs` and `tests/processing_contract.rs`.
2. `cargo clippy --all-targets -- -D warnings` returned exit 101 because Rust 1.99 denies `chunks_exact_to_as_chunks` at the media-brand helper.
3. A disposable deleted-post probe found a real invariant defect in the pre-fix SQL: `publish_processed` returned conflict and left the post unreleased/job running, but `written_media` had already inserted a ready row. Raw state was `release_state=0 ready_media=1 job_state=1 claimed_by=Some("probe-owner")`.

The formatting and Clippy issues were mechanical. The publication defect required a SQL ordering/eligibility correction and committed regression coverage.

## Target-host measurements from the failed gate

### Source open + verify + sniff + rewind

Release-mode, warm-page-cache, fixed 128 KiB verification buffer, 15 operations per size:

- 8 MiB: **35.104 ms average**, **227.91 MiB/s**;
- 64 MiB: **298.757 ms average**, **214.92 MiB/s**;
- 256 MiB: **1,122.598 ms average**, **228.05 MiB/s**.

These remain valid after the correction: the source read/hash/size/path architecture did not change; the only `processing.rs` semantic change is the Clippy-equivalent 4-byte brand iteration helper plus formatting.

### Pre-fix publication baseline

The exact pre-fix publication statement completed c1 1,000/1,000 with zero errors at **1.419 ms average** and **698.34 TPS**. The pre-fix `EXPLAIN (ANALYZE, BUFFERS)` used bounded index access for job/source/media/post and no sequential scan; measured execution was **0.989 ms** on the one-row plan probe.

**Do not retain these publication numbers as the accepted post-fix baseline.** The corrected statement now joins and locks the post before `written_media`, so both the query plan and c1 publication latency must be remeasured in the targeted revalidation.

## Corrective implementation after failed gate

- `processing.rs` now matches the gate-recorded Rust formatting and uses `as_chunks::<4>()` rather than the denied constant-size `chunks_exact(4)` pattern.
- `publication.rs` now makes post eligibility part of the initial owned set and locks both job and post before ready-media insertion.
- `processing_contract.rs` is formatted and includes a committed regression that first soft-deletes the post, then separately sets a non-releasable release state; in both cases publication must return conflict while media count stays zero and the job remains running/owned.

No source-verification architecture, media schema, ingestion code, Go/API/frontend code, codec dependency, Redis path, or polling loop changed.

## Remaining observations

- Standard-library canonicalization + symlink checks prevent traversal and ordinary symlink substitution, but are not an `openat2`/`O_NOFOLLOW` race-proof sandbox against a malicious concurrent local filesystem writer. The media tree is service-controlled; escalate only if that threat model changes.
- Full source SHA-256 verification costs one sequential source read before codec consumption; retain the target measurements above and reassess only with real codec/profile evidence.
- Concrete output-file no-overwrite publication belongs with the first codec implementation.
- The ingestion-side source-orphan janitor/reconciliation gap remains open before production ingestion is enabled.
- Go API and Rust worker still need compatible shared-root UID/GID/permissions in deployment.

## Local-agent evidence workflow

Use isolated/disposable resources and return one ZIP containing exact SHA/status, commands, raw stdout/stderr, environment/tool versions, query plans/measurements, failures, and cleanup/restoration evidence. Preserve remote raw evidence until no longer needed.

## Single best next task

Run a **targeted execution-only revalidation** on the exact feature-branch SHA after this state commit. Require `cargo fmt --check`, `cargo check`, `cargo clippy --all-targets -- -D warnings`, no-DB tests, and the full PostgreSQL worker suite; run the new deleted/non-releasable publication regression plus the publication lock-wait expiry regression repeatedly; rerun `EXPLAIN (ANALYZE, BUFFERS)` and c1/1,000 latency for the corrected publication SQL because that measured path changed. Do not rerun the expensive 8/64/256 MiB source-verification benchmarks or the already-passed source-path/security probes unless the targeted checks expose a broader source-verification issue. Return one evidence ZIP; only a clean PASS should permit fast-forward integration into `v2`.
