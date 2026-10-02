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
- M1 board benchmark is complete.
- M2 fresh schema + core Go API is complete and integrated.
- M3 durable PostgreSQL media-job ownership/recovery boundary is complete and integrated.
- M3 upload/URL-ingestion + durable-enqueue boundary is complete and integrated.
- The worker source/processing contract is isolated on `astra/m3-worker-source-contract`, branched from exact `v2` tip `a552e96954ec2de42523bb5449a52eff0b216ec8`.
- Worker-contract implementation commits before this state update:
  - `febc7b160f7cc905b2c7f2023b7481c6d3b12856` — initial source verification, processing/output contract, fenced publication tests and measurement probe;
  - `24c95f6a8d210d9c173a5e2d6152113c3342da4b` — reduce publication preflight from three PostgreSQL queries to one lock/recheck round trip and document source immutability;
  - `b5e7f537ad361b2d66240392130a01972bae00ba` — harden committed coverage for video dispatch, shard-symlink rejection and mid-read cancellation; use a less fragile one-second publication lock-wait lease.
- Backend v2 lives under `src/backend/v2`; legacy backend is reference-only.
- Worker v2 lives under `src/worker/v2`; legacy `src/worker` is reference-only unless explicitly reviewed for reuse.
- `.local-agent-results/` is ignored for local-agent evidence ZIPs.

## Retained architecture and validated decisions

Keep unless new evidence contradicts them:

- Go 1.25 + pgx/v5 for API/workflows; Rust for media work;
- PostgreSQL authoritative for application/workflow/durable job state;
- no Redis dependency without measured need;
- PostgreSQL pool cap 8 on the Go API until measurements justify more;
- bounded concurrency and explicit deadlines/cancellation;
- post-ID cursor pagination, never OFFSET;
- bounded/indexed hot SQL and target-host measurements before accepting performance-sensitive work;
- immutable numeric relational IDs; usernames never foreign keys;
- local NVMe is the intended media/source store;
- processing is at-least-once; durable external effects must be deterministic/idempotent;
- media-job ownership remains readiness-first `FOR UPDATE SKIP LOCKED` with worker ID + lease-generation fencing and strict post-lock real-time expiry checks;
- initial worker execution concurrency remains one job at a time; no production polling loop or Redis wakeup dependency yet.

## Previously accepted M3 ingestion boundary

The integrated ingestion boundary provides authoritative `media_sources`, bounded source staging outside DB transactions, SHA-256/byte-size integrity metadata, strict `sources/<2 lowercase hex>/<32 lowercase hex>` keys, URL SSRF protection, and one short transaction creating unreleased post + source + pending kind-0 job.

Accepted target baselines:

- 8 MiB source stage + SHA-256 + fsync/publication/removal: **45.1468 ms/op mean, 186.06 MB/s, 67,564.7 B/op, 32 allocs/op** on target-local NVMe;
- PostgreSQL ingestion write on PostgreSQL 17.11: c1 **0.348 ms / 2,870.87 TPS / 0 errors**, c4 **0.457 ms / 8,761.36 TPS / 0 errors**.

The process-crash orphan gap between source-file publication and authoritative ingestion commit remains unresolved until a production-safe janitor/reconciliation path is designed.

## M3 worker source/processing contract — implemented on branch

### Scope and omissions

This slice connects a claimed kind-0 media job to its authoritative source and defines the pre-codec execution/publication contract.

It deliberately does **not** add AVIF/image encoding, video transcoding, production processed-output file writing, a polling/executor loop, concurrency above one, Redis, API/auth changes, or schema changes.

`src/worker/v2/Cargo.toml` adds only `sha2 = "0.10"` for source integrity and `libc = "0.2"` for Unix directory-relative safe opens.

### Fenced source lookup

`ProcessingStore::load_source` requires exact job/post IDs, running state, current worker ID, current lease generation, unexpired lease via `clock_timestamp()`, and kind 0. It loads authoritative `media_sources` metadata and returns explicit outcomes for missing source, unsupported job kind, invalid source metadata, or lost lease.

### Safe bounded source verification

`SourceVerifier`:

- canonicalizes the configured media root and requires a real `sources` directory;
- revalidates exact source-key grammar and shard/ID relationship;
- on Unix, holds the canonical `sources` directory FD open and uses `openat` + `O_NOFOLLOW` for shard and file components;
- requires a regular file and authoritative byte size inside the configured bound;
- reads with a fixed 128 KiB buffer and checks caller cancellation before and between reads;
- computes full SHA-256 and compares it with `media_sources.sha256`;
- keeps only the first 4 KiB during that same hash pass for type sniffing;
- rewinds the same verified open FD for the processor boundary instead of reopening a path.

Errors are classified retryable I/O, terminal input/integrity/path/type, or cancellation.

Published source files are an explicit **application-immutable** contract. A Unix FD is not a snapshot if another trusted process rewrites the inode, so deployment/maintenance/regeneration code must never mutate a published source in place; replacement must use a new durable object/metadata identity.

### Real media-type routing

Declared MIME is never trusted for processor selection. Signature routing recognizes JPEG, PNG, GIF87a/GIF89a, WebP, AVIF, common MP4-family video brands and WebM. Known HEIF/HEIC brands are deliberately not treated as generic MP4 video.

Signature recognition is only routing/security classification. Future codecs must structurally parse/decode the full file and reject malformed/truncated media.

### Explicit processors and deterministic output identity

`ImageProcessor` and `VideoProcessor` are separate traits and both dispatch paths have committed unit coverage.

Recipe version 1 derives the deterministic primary output key from post ID + verified source SHA-256 + media class:

`media/v1/<image|video>/<source-hash-shard>/<post-id>-<source-sha256>`

The key is extensionless; authoritative output MIME stays in PostgreSQL. Any recipe/codec change that can alter durable output bytes/semantics must bump the recipe version.

`ProcessedMedia` must match its plan's media class/key and provide valid dimensions, duration, byte size, SHA-256, MIME and optional perceptual hash.

Production output-file publication is intentionally not implemented yet. Future codecs must atomically/idempotently publish the deterministic file key before DB publication; ambiguous DB commit must not trigger deletion of an output that may already be referenced.

### Fenced PostgreSQL publication transaction

`ProcessingStore::publish_processed` runs only after verification/processor work; no DB transaction spans source hashing or codec work.

The short transaction uses:

1. one preflight SQL round trip whose materialized CTEs lock the exact current job/post and authoritative source row, then evaluate `clock_timestamp()` lease validity after those lock waits;
2. source SHA-256 comparison against the verified plan;
3. one data-modifying CTE that writes/verifies the ready `media` row, releases the non-deleted post, and completes the same worker/generation-fenced job;
4. a second strict `lease_expires_at > clock_timestamp()` fence in the final job update;
5. rollback if output conflicts, source changes/disappears, post is unavailable, or final completion loses/expires its lease.

The `media` upsert accepts an existing row only when all durable output metadata is identical. If the write CTE waits long enough to cross lease expiry, media/post changes remain uncommitted and `job_completed = false` causes the caller to roll the whole transaction back.

### Committed coverage

Unit coverage includes supported/unsupported signatures; HEIC not becoming MP4; byte-size/SHA mismatch; pre-read and between-read cancellation; strict key validation; Unix file-symlink and shard-symlink rejection; deterministic output identity; image dispatch; video dispatch; and output-plan validation.

PostgreSQL integration coverage in `tests/processing_contract.rs` includes fenced source load; atomic media + post release + job success; already-expired rollback; a one-second lease publication that is observed blocked before expiry and released only after real expiry; stale-generation rejection/current-generation success; changed-source rejection; and conflicting-media rollback.

The existing durable job-state integration suite remains unchanged and must continue to pass.

### Target measurement probe

Ignored `measure_source_open_hash_sniff_8mib` measures safe directory-relative open + full SHA-256 + first-4-KiB sniff + rewind for an 8 MiB disposable source. Fixture creation/sync is outside the timed section. It is a pre-codec verification baseline, not a codec-throughput benchmark.

## Validation status

This ChatGPT runtime has no `rustc`/`cargo` or disposable PostgreSQL target environment. Therefore **no Rust fmt/compile/Clippy/test or target-performance claim is made for this branch yet**.

The implementation has received structural review here, including reduction of publication preflight work, but must pass the execution-only Rust/PostgreSQL/target-filesystem gate before integration into `v2`.

## Correctness/performance decisions

- Hash and sniff in one source pass; do not reread merely to choose processor family.
- Keep only a 4 KiB sniff prefix and fixed 128 KiB read buffer; do not buffer whole uploads in worker memory.
- Keep source verification and codec work outside DB transactions.
- Use one publication preflight DB round trip while retaining explicit row locks and post-lock real-time expiry evaluation.
- Keep an explicit short DB transaction so partial media/post writes can be rolled back if the final strict job fence fails after another lock wait.
- Treat published source storage as immutable.
- Keep output identity deterministic and recipe-versioned before choosing codecs.
- Do not optimize AVIF/video before this execution contract and its baseline costs are validated.

## Remaining observations before codec work

- Real codec structural validation/encoding is not implemented.
- Atomic filesystem publication for processed outputs is specified but not implemented.
- No production executor/polling loop exists, so `ProcessingError` disposition is not yet wired to `JobStore::fail` retry scheduling.
- Cancellation is checked between local filesystem reads; a blocking local read is not forcibly interrupted mid-syscall.
- Source-file immutability must be maintained operationally; current ingestion modes are `0640` files / `0750` directories.
- Source-ingestion orphan reconciliation remains required before production ingestion is enabled.
- No target baseline yet exists for source verification or the fenced publication transaction.

## Local-agent evidence workflow

For target-host work, use isolated/disposable resources and return one ZIP with exact SHA/status, commands, raw stdout/stderr, environment/tool versions, measurements, failures, and cleanup/restoration evidence. Preserve raw remote evidence until no longer needed.

## Single best next task

Run an **execution-only validation gate** against the exact `astra/m3-worker-source-contract` SHA after this state-file commit: Rust fmt/check/Clippy/unit tests; full PostgreSQL 17 integration tests including existing job fencing and the new publication lock-wait/stale-generation/rollback cases; Linux safe-open/file+shard-symlink/integrity/cancellation checks; the target-local 8 MiB source-open/hash/sniff measurement; and a disposable PostgreSQL baseline for the exact two-round-trip fenced publication transaction. If any gate fails, fix this branch before considering fast-forward integration into `v2`.