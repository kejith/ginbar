# Ginbar v2 State / Handoff

Last updated: 2026-10-03
Phase: M3 media pipeline integration — worker source-consumption/publication contract IMPLEMENTED ON BRANCH; validation pending
Integration branch: `v2`
Active M3 branch: `astra/m3-worker-processing-contract`
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the resume point. Read it before `PLAN.md`. Do not rely on chat history as project memory.

## Branch status

- `master` remains read-only for rewrite work.
- `v2` remains at `a552e96954ec2de42523bb5449a52eff0b216ec8`.
- M1 board benchmark is complete.
- M2 fresh schema + core Go API is complete and integrated.
- M3 durable PostgreSQL media-job ownership/recovery boundary is complete and integrated.
- M3 upload/URL-ingestion + durable-enqueue boundary is complete and integrated.
- The worker processing-contract slice is isolated on `astra/m3-worker-processing-contract`, branched from exact `v2` tip `a552e96954ec2de42523bb5449a52eff0b216ec8`.
- This slice changes only `src/worker/v2/**` plus this handoff document.
- Worker README now matches the source-bound publication contract described below.
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

Accepted target baselines:

- 8 MiB source staging durability path: mean **45.1468 ms/op**, **186.06 MB/s**, ~67.6 KiB/op, 32 allocs/op;
- PostgreSQL ingestion CTE c1: **0.348 ms**, **2,870.87 TPS**, zero errors;
- PostgreSQL ingestion CTE c4: **0.457 ms**, **8,761.36 TPS**, zero errors.

## M3 worker source-consumption / publication contract — implemented on branch

### Scope

This slice connects claimed kind-0 jobs to a verified source/processor/publication contract. It deliberately does **not** implement real AVIF/image encoding, ffmpeg/video processing, perceptual duplicate policy, regeneration, a production polling loop, or progress UI.

No Go/API/frontend code changes in this slice.

### Source loading and verification

`src/worker/v2/src/processing.rs` adds:

- authoritative `media_sources` lookup by immutable `post_id`;
- `prepare_claimed_source` for claimed kind-0 jobs;
- exact ingestion-key grammar validation;
- shared-root canonical containment checks;
- rejection of symlink source files and shard directories;
- caller-supplied maximum source-byte bound;
- filesystem-size vs DB-size verification;
- fixed 128 KiB streaming buffer;
- SHA-256 verification using new dependency `sha2 = "0.10"`;
- cancellation checks during full verification read;
- actual media-family sniffing instead of trusting declared MIME;
- rewind of the same verified file handle before processor dispatch.

Recognized routing families: JPEG, PNG, GIF, WebP, AVIF/HEIF-family ISO-BMFF images, MP4-family video, and EBML-family video. This is dispatch classification, not full decoder validation.

### Error classification

Retryable:

- PostgreSQL/filesystem I/O;
- cancellation during verification.

Terminal:

- missing source;
- unsupported job kind;
- invalid source digest shape/key/path;
- size or SHA-256 mismatch;
- unsupported/unrecognized media type.

The future production worker loop can map this to the existing durable retry/fail transitions.

### Processor boundaries

`ImageProcessor` and `VideoProcessor` traits receive a verified, rewound source file handle. `dispatch` routes only by sniffed media family. No concrete codecs are implemented yet.

This avoids an unnecessary reopen between verify and decode. Codec consumption still performs another read after the verification pass; target measurement is required before deciding whether a later architecture should combine verification with decode for very large media.

### Deterministic processed-output identity

`processed_output_key` defines:

`media/<post shard>/<post id>/v<processing version>-<source SHA-256>.<format>`

Identity includes immutable post ID, authoritative source digest, processing-contract version, and output format. Validation rejects wrong post/shard/version/digest/extension shapes.

Future processors must durably publish output bytes under this deterministic identity before returning `ProcessedMedia`. Concrete output-file staging/fsync/no-overwrite collision verification is intentionally deferred to the first codec implementation, but the contract is explicit: retries may reuse an existing deterministic object only after proving it is the same result and must never overwrite unrelated bytes.

### Fenced processed-media publication

`src/worker/v2/src/publication.rs` adds `publish_processed` as the short authoritative DB commit point after output-file durability.

One data-modifying PostgreSQL statement:

1. identifies the exact kind-0 running job by job ID, post ID, worker ID and lease generation;
2. requires the authoritative `media_sources.sha256` to equal the verified source digest supplied by the worker;
3. locks the job row and rechecks lease expiry against `clock_timestamp()` after lock acquisition;
4. inserts ready `media` (`processing_state = 1`), or accepts an existing row only when deterministic storage key and output SHA-256 are identical;
5. releases the post only if the ready media write produced a row;
6. marks the fenced job succeeded and clears ownership only if publication + release succeeded.

Rust-side publication validation also requires the deterministic output key to embed the same verified source digest.

An expired/stale lease, source-identity change, deleted/non-releasable post, or conflicting prior media identity yields `LeaseLostOrConflict` without releasing the post or succeeding the job. No DB transaction spans source hashing, decode/encode, or filesystem output work.

### Committed tests

Unit coverage in `processing.rs` includes:

- exact source-key grammar;
- media-family sniff signatures;
- byte-size/hash/type verification and rewind;
- terminal integrity vs retryable cancellation classification;
- deterministic/versioned output keys;
- image/video dispatch boundary.

PostgreSQL-backed `tests/processing_contract.rs` covers:

- authoritative source lookup;
- kind-0 job claim;
- real file verification from a disposable media root;
- successful media write + post release + job success/ownership clear;
- authoritative source-digest change blocking publication;
- already-expired publication rejection;
- publication blocked on the job row until after lease expiry, verifying post-lock expiry rejection.

Existing durable-job tests remain unchanged.

## Correctness/performance decisions

- Keep source verification and codec work outside DB transactions.
- Reuse the verified file handle rather than reopen before dispatch.
- Keep full source SHA-256 verification because ingestion records an authoritative digest; measure its cost before redesigning.
- Keep media sniffing bounded and separate from full codec parsing.
- Keep image/video processor traits explicit and codec-agnostic.
- Keep output identities deterministic/versioned for at-least-once retries.
- Bind final publication to the verified source digest and the still-authoritative DB source identity without another round trip.
- Fence final DB publication itself; a lease valid before a row-lock wait is insufficient.
- Never silently replace a different existing media identity for the same post.
- No Redis is justified.

## Validation status

This branch is **not integrated** into `v2`.

The current ChatGPT runtime has no Rust/Cargo toolchain and no real PostgreSQL target environment, so no claim is made that this implementation compiles or passes DB tests yet. Target execution is required.

`src/worker/Cargo.lock` remains ignored by existing repository policy; the gate must record the resolved `sha2` version/toolchain.

## Remaining / next-gate observations

- Standard-library canonicalization + symlink rejection prevents DB/path traversal and ordinary symlink substitution, but is not an `openat2`/`O_NOFOLLOW` race-proof sandbox. The media tree is service-controlled; escalate only if the threat model grants an untrusted local writer concurrent access.
- Full SHA-256 verification adds one sequential source read before codec consumption. Benchmark source open + hash + sniff + rewind on target local NVMe for representative sizes.
- Concrete output-file no-overwrite publication belongs with the first real codec slice.
- The ingestion-side source-orphan janitor/reconciliation gap remains open before production ingestion is enabled.
- Go API and Rust worker still need compatible shared-root UID/GID/permissions in deployment.

## Local-agent evidence workflow

Use isolated/disposable resources and return one ZIP containing exact SHA/status, commands, raw stdout/stderr, environment/tool versions, query plans/measurements, failures, and cleanup/restoration evidence. Preserve remote raw evidence until no longer needed.

## Single best next task

Run the execution-only M3 worker processing-contract validation gate against the exact feature-branch SHA after this state-file commit: `cargo fmt --check`, `cargo check`, `cargo clippy --all-targets -- -D warnings`, no-DB tests, full PostgreSQL-backed worker tests (including source-identity and publication lock-wait regressions), source-path/symlink/integrity probes, `EXPLAIN (ANALYZE, BUFFERS)` for fenced publication, and target-host source open + SHA-256 + sniff + rewind benchmarks on representative local-NVMe sizes. Return one raw-evidence ZIP. If any gate fails, fix this branch before considering fast-forward integration into `v2`.
