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
- Implementation commits before this state update:
  - `febc7b160f7cc905b2c7f2023b7481c6d3b12856` — source verification, processing/output contract, fenced publication tests and measurement probe;
  - `24c95f6a8d210d9c173a5e2d6152113c3342da4b` — reduce publication preflight from three PostgreSQL queries to one lock/recheck round trip and document source immutability.
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

The integrated ingestion boundary provides:

- authoritative `media_sources` rows for upload/URL inputs;
- bounded source staging outside DB transactions;
- SHA-256 and exact source byte size;
- strict source-key grammar `sources/<2 lowercase hex>/<32 lowercase hex>`;
- URL SSRF protection and untrusted declared MIME;
- one short PostgreSQL transaction/CTE creating unreleased post + source + pending kind-0 job.

Accepted target baselines from that slice:

- 8 MiB source stage + SHA-256 + file/directory fsync + publication + removal: **45.1468 ms/op mean, 186.06 MB/s, 67,564.7 B/op, 32 allocs/op** on target-local NVMe;
- PostgreSQL ingestion write on PostgreSQL 17.11: c1 **0.348 ms / 2,870.87 TPS / 0 errors**, c4 **0.457 ms / 8,761.36 TPS / 0 errors**.

The process-crash orphan gap between source-file publication and authoritative ingestion commit remains intentionally unresolved until a production-safe janitor/reconciliation path is designed.

## M3 worker source/processing contract — implemented on branch

### Scope and intentional omissions

This slice connects a claimed kind-0 media job to its authoritative source and defines the execution/publication contract before real codecs.

It deliberately does **not** add:

- AVIF/image encoding;
- video transcoding;
- production output-file writer/publication code;
- production polling/executor loop;
- worker concurrency above one;
- Redis;
- API/auth changes;
- schema changes.

`src/worker/v2/Cargo.toml` adds only `sha2 = "0.10"` for source integrity verification and `libc = "0.2"` for Unix directory-relative safe opens.

### Fenced source lookup

`ProcessingStore::load_source` requires:

- exact job ID and post ID;
- running job state;
- current worker ID;
- current lease generation;
- unexpired lease via `clock_timestamp()`;
- job kind 0.

It loads the authoritative `media_sources` row and validates basic DB metadata/origin consistency before filesystem work. Missing source, unsupported job kind, invalid source metadata, and lost lease are explicit outcomes.

### Safe bounded source verification

`SourceVerifier`:

- canonicalizes the configured media root and requires a real `sources` directory;
- revalidates the exact integrated source-key grammar and shard/ID relationship;
- on Unix, holds the canonical `sources` directory FD open and uses `openat` with `O_DIRECTORY`/`O_NOFOLLOW` for the shard plus `O_NOFOLLOW` for the source file, so untrusted path components are never followed as symlinks;
- requires a regular file;
- rejects DB byte sizes outside the configured verification bound before reading;
- checks opened-file length against the authoritative byte size;
- reads with a bounded 128 KiB buffer and checks caller cancellation between reads;
- computes full SHA-256 and compares it with `media_sources.sha256`;
- captures only the first 4 KiB during the same hash pass for type sniffing, avoiding a second source scan;
- rewinds the same verified open file handle for the processor boundary instead of reopening the path.

Errors are classified as retryable I/O, terminal input/integrity/path/type errors, or cancellation.

Published source files are now an explicit **application-immutable** contract. Passing the same open FD from verification to processing avoids a path-reopen race, but an FD is not a filesystem snapshot if another trusted process rewrites that inode. Deployment/maintenance/regeneration code must never mutate a published source in place; replacement must use a new durable object/metadata identity.

### Real media-type routing

Declared MIME is never used to select a processor.

Current signature routing recognizes:

- JPEG;
- PNG;
- GIF87a/GIF89a;
- WebP;
- AVIF;
- common MP4-family video brands;
- WebM.

Known HEIF/HEIC brands are deliberately not treated as generic MP4 video.

This is only an initial routing/security boundary. Future codecs must structurally parse/decode the complete file and reject malformed/truncated content even when its leading signature is recognized.

### Explicit processor boundaries and deterministic output identity

`ImageProcessor` and `VideoProcessor` are separate traits. `dispatch_verified_source` routes from the sniffed type only.

Recipe version 1 derives the deterministic primary output key from post ID + verified source SHA-256 + media class:

`media/v1/<image|video>/<source-hash-shard>/<post-id>-<source-sha256>`

The key is extensionless; authoritative output MIME is PostgreSQL metadata. Any future recipe/codec change that may change durable output bytes or semantics must bump the recipe version rather than reuse another recipe's identity.

`ProcessedMedia` must match the plan's media class and storage key and provide valid dimensions, duration, byte size, SHA-256, MIME and optional perceptual hash.

External output-file publication is intentionally not implemented in this slice. Future codecs must publish the deterministic file key atomically/idempotently before the DB publication transaction; an ambiguous DB commit must not trigger deletion of an output that may already be referenced.

### Fenced PostgreSQL publication transaction

`ProcessingStore::publish_processed` runs only after source verification/processor work and holds no DB transaction during hashing/codec work.

The short publication transaction uses:

1. one preflight SQL round trip whose materialized CTEs lock the exact current job/post and authoritative source row, then evaluate `clock_timestamp()` lease validity after those lock waits;
2. source SHA-256 comparison against the verified plan;
3. one data-modifying CTE that writes/verifies the ready `media` row, releases the non-deleted post, and completes the same worker/generation-fenced job;
4. a second strict `lease_expires_at > clock_timestamp()` fence in the final job update;
5. transaction rollback if output conflicts, source changed/disappeared, post is unavailable, or final job completion loses/expires its lease.

The `media` upsert accepts an existing row only if **all** durable output metadata is identical. A retry therefore cannot silently bless nondeterministic output under the deterministic key.

If the write CTE waits on another DB row long enough to cross lease expiry, its media/post changes remain uncommitted; `job_completed = false` causes the caller to roll the entire transaction back.

### Committed coverage

Unit tests cover:

- supported/unsupported signature sniffing including HEIC not becoming MP4;
- byte-size and SHA-256 integrity failures;
- source cancellation classification;
- strict source-key validation;
- Unix source symlink rejection;
- deterministic output-plan identity;
- image processor dispatch and output-plan validation.

PostgreSQL integration tests in `tests/processing_contract.rs` cover:

- source lookup requires the current fenced lease;
- atomic ready-media + post release + job completion;
- already-expired publication leaves no media and keeps the post unreleased;
- publication that begins unexpired but blocks on the job row until after real expiry loses the lease and rolls back;
- stale generation cannot publish after reclaim while the current generation can;
- changed source SHA-256 is rejected;
- conflicting preexisting media is rejected without releasing the post.

The existing durable job-state integration suite remains unchanged and must continue to pass.

### Target measurement probe

Ignored test `measure_source_open_hash_sniff_8mib` measures the new pre-codec verification path over an 8 MiB disposable source:

- safe directory-relative open;
- full SHA-256;
- first-4-KiB signature sniff;
- rewind.

Fixture creation/sync is outside the timed section. This probe establishes source-verification overhead only; it is not a codec-throughput benchmark.

## Validation status

The current ChatGPT execution runtime does not contain `rustc`/`cargo` or a disposable PostgreSQL target environment. Therefore **no Rust compile/fmt/Clippy/test claim is made for this branch yet**.

The implementation has been reviewed structurally here, including the post-implementation round-trip reduction, but the branch must pass an execution-only Rust/PostgreSQL/target-filesystem gate before integration into `v2`.

## Correctness/performance decisions

- Hash and sniff in one source pass; do not reread the source merely to identify its processor family.
- Keep only a 4 KiB sniff prefix and a fixed 128 KiB read buffer; do not buffer the whole upload in worker memory.
- Keep source verification and codec work outside DB transactions.
- Reduce publication preflight to one PostgreSQL round trip while retaining explicit row locks and post-lock real-time expiry evaluation.
- Keep an explicit short DB transaction for publication so partial media/post changes can be rolled back if the final strict job fence fails after another lock wait.
- Treat source storage as immutable after ingestion; do not design worker correctness around mutable source inodes.
- Keep output identity deterministic and recipe-versioned before choosing codecs.
- Do not optimize AVIF/video until source verification, fencing, publication and baseline costs are validated.

## Remaining observations before codec work

- Real codec structural validation and encoding are not implemented.
- Atomic filesystem publication for processed outputs is specified but not implemented.
- There is no production executor/polling loop yet, so `ProcessingError` disposition is not yet wired to `JobStore::fail` retry scheduling.
- Cancellation is checked between local file reads; ordinary blocking local filesystem reads are not forcibly interrupted mid-syscall.
- Source-file immutability must be preserved operationally; current ingestion modes are `0640` files / `0750` directories.
- Source-ingestion orphan reconciliation remains required before production ingestion is enabled.
- No source-verification or publication-transaction target baseline exists yet.

## Local-agent evidence workflow

For target-host work, use isolated/disposable resources and return one ZIP with exact SHA/status, commands, raw stdout/stderr, environment/tool versions, measurements, failures, and cleanup/restoration evidence. Preserve raw remote evidence until no longer needed.

## Single best next task

Run an **execution-only validation gate** against the exact `astra/m3-worker-source-contract` SHA after this state-file commit: Rust fmt/check/Clippy/unit tests; full PostgreSQL 17 integration tests including existing job fencing and the new publication lock-wait/stale-generation/rollback cases; Linux safe-open/symlink/integrity/cancellation probes; the target-local 8 MiB source-open/hash/sniff measurement; and a disposable PostgreSQL baseline for the exact two-round-trip fenced publication transaction. If any gate fails, fix this branch before considering fast-forward integration into `v2`.