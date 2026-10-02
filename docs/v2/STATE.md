# Ginbar v2 State / Handoff

Last updated: 2026-10-03
Phase: M3 media pipeline integration — worker source/processing contract IMPLEMENTED ON BRANCH; validation pending
Integration branch: `v2`
Active M3 branch: `astra/m3-worker-source-contract`
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the resume point. Read it before `PLAN.md`. Do not rely on chat history as project memory.

## Branch status

- `master` remains untouched by rewrite work at `181fa44d79c7b4a1984c1a35795762dd503b3f77`.
- `v2` remains at `a552e96954ec2de42523bb5449a52eff0b216ec8`.
- M1 board benchmark and M2 fresh schema/core Go API are complete and integrated.
- M3 durable PostgreSQL media-job ownership/recovery and upload/URL-ingestion + durable-enqueue boundaries are complete and integrated.
- The worker source/processing contract is isolated on `astra/m3-worker-source-contract`, based on exact `v2` tip `a552e96954ec2de42523bb5449a52eff0b216ec8`.
- Worker-contract commits before this state update:
  - `febc7b160f7cc905b2c7f2023b7481c6d3b12856` — initial source verification, processor/output contract, fenced DB publication and tests;
  - `24c95f6a8d210d9c173a5e2d6152113c3342da4b` — reduce publication preflight from three PostgreSQL queries to one lock/recheck round trip; document source immutability;
  - `b5e7f537ad361b2d66240392130a01972bae00ba` — add video dispatch, shard-symlink, mid-read cancellation coverage and less fragile publication lock-wait timing;
  - `d1a3e58890cba393089af2abfd76bd98fda17448` — add regression for crossing lease expiry while the publication write waits on an existing `media` row.
- Backend v2 lives under `src/backend/v2`; worker v2 under `src/worker/v2`; legacy code is reference-only unless explicitly reviewed.
- `.local-agent-results/` is ignored for evidence ZIPs.

## Retained architecture and validated decisions

- Go 1.25 + pgx/v5 for API/workflows; Rust for media work.
- PostgreSQL is authoritative for application/workflow/durable job state; no Redis without measured need.
- Bounded concurrency/deadlines; immutable numeric relational IDs; post-ID cursor pagination, never OFFSET.
- Local NVMe is the intended media/source store.
- Processing is at-least-once; durable external effects must be deterministic/idempotent.
- Media-job ownership remains readiness-first `FOR UPDATE SKIP LOCKED` with worker ID + lease-generation fencing and strict post-lock `clock_timestamp()` expiry checks.
- Initial worker execution concurrency remains one job; no production polling loop yet.

## Previously accepted ingestion boundary

Integrated ingestion provides authoritative `media_sources`, bounded staging outside DB transactions, SHA-256/byte-size metadata, exact `sources/<2 lowercase hex>/<32 lowercase hex>` keys, URL SSRF protection, and one short transaction creating unreleased post + source + pending kind-0 job.

Accepted target baselines:

- 8 MiB source staging durability path: **45.1468 ms/op mean, 186.06 MB/s, 67,564.7 B/op, 32 allocs/op** on target-local NVMe.
- PostgreSQL 17.11 ingestion write: c1 **0.348 ms / 2,870.87 TPS / 0 errors**, c4 **0.457 ms / 8,761.36 TPS / 0 errors**.

The source-file orphan gap between file publication and authoritative ingestion commit still requires a production-safe janitor/reconciliation design.

## M3 worker source/processing contract — implemented on branch

### Scope

This slice connects a claimed kind-0 media job to its authoritative source and defines the execution/publication contract before real codecs.

It intentionally does **not** implement AVIF/image encoding, video transcoding, processed-output file writing, a production executor/polling loop, concurrency above one, Redis, API/auth changes or schema changes.

`Cargo.toml` adds only `sha2 = "0.10"` and `libc = "0.2"`.

### Fenced source lookup

`ProcessingStore::load_source` requires exact job/post IDs, running state, current worker ID, current lease generation, an unexpired lease and job kind 0. It loads `media_sources` and returns explicit missing-source / unsupported-kind / invalid-metadata / lease-lost outcomes.

### Safe bounded verification

`SourceVerifier`:

- canonicalizes media root and requires a real `sources` directory;
- revalidates exact source-key grammar;
- on Unix uses a held `sources` directory FD plus `openat` with `O_NOFOLLOW` for shard and file components;
- requires a regular file and authoritative size inside the configured bound;
- reads with a fixed 128 KiB buffer and checks caller cancellation before/between reads;
- computes full SHA-256 and compares it with `media_sources.sha256`;
- keeps only the first 4 KiB during that same pass for signature sniffing;
- rewinds the same verified open FD for processor use instead of reopening the path.

Errors are classified retryable I/O, terminal input/integrity/path/type, or cancellation.

Published source files are an **application-immutable** contract. An open FD is not a snapshot if another trusted process rewrites the inode; deployment/maintenance/regeneration code must never mutate published sources in place.

### Type routing

Declared MIME never selects the processor. Routing recognizes JPEG, PNG, GIF, WebP, AVIF, common MP4-family video and WebM. HEIF/HEIC brands are deliberately not treated as generic MP4 video.

Sniffing is only a routing/security boundary; future codecs must structurally parse/decode the complete file.

### Processor/output contract

`ImageProcessor` and `VideoProcessor` are separate traits and both dispatch paths have committed coverage.

Recipe-v1 deterministic primary key:

`media/v1/<image|video>/<source-hash-shard>/<post-id>-<source-sha256>`

Output MIME is PostgreSQL metadata; recipe/codec changes that alter durable output semantics must bump the recipe version. `ProcessedMedia` must match the plan and provide valid dimensions/duration/size/SHA/MIME/phash metadata.

Processed-file publication is intentionally not implemented yet. Future codecs must atomically/idempotently publish the deterministic file key before DB publication; ambiguous DB commit must not trigger deletion of a potentially referenced output.

### Fenced DB publication

`ProcessingStore::publish_processed` starts only after source verification/processor work, so no DB transaction spans hashing/codec work.

The short transaction:

1. performs one preflight SQL round trip whose materialized CTEs lock the exact current job/post and source row, then evaluate real-time lease validity after those locks;
2. requires current source SHA to match the verified plan;
3. executes one data-modifying CTE that writes/verifies the ready `media` row, releases the non-deleted post, and completes the same worker/generation-fenced job;
4. rechecks strict lease expiry in the final job update;
5. rolls the whole transaction back on output conflict, source change/disappearance, post unavailability, or lease loss/expiry.

The media upsert accepts an existing row only if all durable metadata is identical.

Two separate lock-wait regressions cover strict expiry:

- expiry while waiting for the preflight job-row lock must produce `LeaseLost` with no media/release;
- expiry after successful preflight while the write CTE waits on an existing `media` row must also produce `LeaseLost`; uncommitted release/completion must roll back and the preexisting media row must remain intact.

### Committed coverage

Unit coverage includes signature families/unknown type/HEIC exclusion, size/hash mismatch, pre-read and between-read cancellation, strict key validation, Unix file+shard symlink rejection, deterministic output identity, image/video dispatch and output-plan validation.

PostgreSQL coverage includes fenced source load, atomic ready-media + release + job success, already-expired rollback, preflight-lock expiry, output-row-lock expiry, stale-generation rejection/current-generation success, changed-source rejection and conflicting-media rollback. Existing job-state fencing tests remain unchanged and must continue to pass.

### Target measurement probe

Ignored `measure_source_open_hash_sniff_8mib` measures safe open + full SHA-256 + first-4-KiB sniff + rewind over an 8 MiB disposable source; fixture creation/sync is outside timing. This establishes pre-codec verification overhead only.

## Validation status

This ChatGPT runtime has no `rustc`/`cargo` or disposable PostgreSQL target environment. **No Rust fmt/compile/Clippy/test or target-performance claim is made yet.**

The implementation has received structural review here and must pass an execution-only Rust/PostgreSQL/target-filesystem gate before integration into `v2`.

## Correctness/performance decisions

- Hash + sniff in one pass; keep only 4 KiB sniff data and a fixed 128 KiB buffer.
- Keep verification/codec work outside DB transactions.
- Use one publication preflight round trip, then one write CTE inside a short rollback-capable transaction.
- Preserve strict real-time fencing both before and inside the write path.
- Treat source storage as immutable.
- Use deterministic recipe-versioned output identity before choosing codecs.
- Do not optimize AVIF/video before this execution contract and baseline costs are validated.

## Remaining observations before codec work

- Real codec validation/encoding and atomic processed-file publication are not implemented.
- No production executor/polling loop exists, so `ProcessingError` disposition is not yet wired to `JobStore::fail` retry scheduling.
- Cancellation is checked between local reads; a blocking local read is not forcibly interrupted mid-syscall.
- Source immutability must be maintained operationally; current ingestion modes are `0640` files / `0750` dirs.
- Source-ingestion orphan reconciliation remains required before production ingestion.
- No target baseline exists yet for source verification or fenced publication.
- No `Cargo.lock` is currently tracked for the worker crate; validation must capture exact resolved dependency versions in a disposable build copy, and production hardening should revisit locking/reproducibility.

## Local-agent evidence workflow

Use isolated/disposable resources and return one ZIP with exact SHA/status, commands, raw outputs, versions, measurements, failures, cleanup/restoration evidence and generated dependency-resolution evidence. Preserve raw remote evidence until no longer needed.

## Single best next task

Run an **execution-only validation gate** against the exact `astra/m3-worker-source-contract` SHA after this state-file commit: Rust fmt/check/Clippy/unit tests; full PostgreSQL 17 integration tests including existing job fencing plus both new publication lock-wait expiry paths; Linux safe-open/file+shard-symlink/integrity/cancellation checks; target-local 8 MiB source-open/hash/sniff measurement; and a disposable PostgreSQL baseline for the exact two-round-trip fenced publication transaction. If any gate fails, fix this branch before fast-forward integration into `v2`.