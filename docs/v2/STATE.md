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
- The next M3 worker processing-contract slice is isolated on `astra/m3-worker-processing-contract`, branched from exact `v2` tip `a552e96954ec2de42523bb5449a52eff0b216ec8`.
- Implementation commits before this state update include source verification/dispatch, fenced publication, DB integration tests, README contract documentation, and a publication lock-wait expiry regression.
- Backend v2 lives under `src/backend/v2`; legacy backend is reference-only.
- Worker v2 lives under `src/worker/v2`; legacy `src/worker` is reference-only unless explicitly reviewed for reuse.
- `.local-agent-results/` is ignored for local-agent evidence ZIPs.

## Retained architecture and validated decisions

Keep unless new evidence contradicts them:

- Go 1.25 + pgx/v5 + standard `net/http` for API/workflows;
- PostgreSQL authoritative for app/workflow/durable job state;
- Rust worker for media processing;
- local NVMe for media/source storage;
- no Redis dependency without measured need;
- small PostgreSQL pools/concurrency until measurements justify more;
- post-ID cursor pagination, never OFFSET;
- immutable numeric relational IDs;
- processing is at-least-once and durable external effects must be deterministic/idempotent;
- initial production media-worker target remains one job at a time.

The integrated media-job boundary retains readiness-first `FOR UPDATE SKIP LOCKED` claiming, lease-generation fencing, post-lock real-time expiry checks, bounded retries, and no production polling loop yet.

## Integrated ingestion/enqueue boundary

The prior M3 slice is integrated and validated:

- upload/URL bytes stage outside DB transactions;
- authoritative `media_sources` rows record source identity/size/SHA-256;
- unreleased post + source + initial kind-0 job are created atomically;
- URL fetches enforce SSRF restrictions;
- source keys use exact `sources/<2 lowercase hex>/<32 lowercase hex>` grammar;
- ambiguous DB commit preserves source bytes rather than risking a committed row pointing at deleted data.

Accepted target baselines from that slice:

- 8 MiB local source staging durability path: mean **45.1468 ms/op**, **186.06 MB/s**, ~67.6 KiB/op, 32 allocs/op;
- PostgreSQL ingestion CTE c1: **0.348 ms**, **2,870.87 TPS**, zero errors;
- PostgreSQL ingestion CTE c4: **0.457 ms**, **8,761.36 TPS**, zero errors.

## M3 worker source-consumption / publication contract — implemented on branch

### Scope and intentional omissions

This slice connects the already-integrated durable kind-0 job/source model to an explicit Rust processing contract. It deliberately does **not** implement real AVIF/image encoding, ffmpeg/video processing, perceptual duplicate policy, regeneration, a production polling loop, or progress UI.

No HTTP/API behavior changes in this slice.

### Source loading and verification

`src/worker/v2/src/processing.rs` adds:

- authoritative `media_sources` lookup by immutable `post_id`;
- `prepare_claimed_source` for claimed kind-0 jobs;
- exact source-key grammar validation matching ingestion;
- shared-root path resolution with canonical containment checks;
- rejection of symlink source files and shard directories;
- caller-supplied maximum source byte bound;
- filesystem-size vs authoritative DB-size verification;
- fixed 128 KiB streaming buffer;
- SHA-256 verification using `sha2`;
- cancellation checks during the full verification read;
- actual media-family sniffing from bytes rather than declared MIME;
- rewind of the same verified file handle before processor dispatch.

Recognized dispatch families currently include JPEG, PNG, GIF, WebP, AVIF/HEIF-family ISO-BMFF images, MP4-family video, and EBML-family video. This is only a family-routing boundary; real codecs must still parse/validate their inputs.

### Error classification

The source/preparation boundary exposes retryable vs terminal classification:

- DB/filesystem I/O and cancellation are retryable;
- missing source, invalid job kind, invalid digest shape, path/key violations, size/hash mismatch, and unsupported media type are terminal.

The eventual production worker loop can map this directly onto the already-integrated durable job retry/fail transitions.

### Explicit processor boundaries

`ImageProcessor` and `VideoProcessor` traits receive a verified, rewound source file handle. `dispatch` routes only on sniffed media family. There are no concrete codec implementations yet.

This keeps source correctness/fencing separate from codec selection and avoids opening a second source handle between verification and decode. The codec will still read the source after the verification pass; target measurement is required before deciding whether later architecture should combine verification with decode for large media.

### Deterministic processed-output identity

`processed_output_key` defines versioned deterministic identities:

`media/<post shard>/<post id>/v<processing version>-<source SHA-256>.<format>`

The key includes immutable post ID, authoritative source digest, processing-contract version, and output format. Validation rejects wrong post/shard/version/digest/extension shapes.

Future processors must durably publish output bytes under this identity before returning `ProcessedMedia`. Output-file writes are not implemented in this slice; the required contract is no-overwrite/idempotent publication. A retry may reuse an existing deterministic object only after proving it is the same result and must never replace unrelated bytes in place.

### Fenced processed-media publication

`src/worker/v2/src/publication.rs` adds `publish_processed` as the short authoritative DB commit point after output-file durability.

One data-modifying SQL statement:

1. locks the exact kind-0 running job by job ID, post ID, worker ID and lease generation;
2. rechecks lease expiry against `clock_timestamp()` **after** the row lock is obtained;
3. inserts ready `media` (`processing_state = 1`), or accepts an existing row only when deterministic storage key and output SHA-256 are identical;
4. releases the post only if the ready media write produced a row;
5. marks the fenced job succeeded and clears ownership only if publication + release succeeded.

An expired/stale lease, deleted/non-releasable post, or conflicting prior media identity yields `LeaseLostOrConflict` without releasing the post or succeeding the job. No PostgreSQL transaction spans source hashing, decode/encode, or filesystem output work.

### Committed tests

Unit coverage in `processing.rs` includes:

- exact source-key grammar;
- supported image/video-family sniff signatures;
- byte-size/hash/type verification and rewind;
- terminal integrity vs retryable cancellation classification;
- deterministic/versioned processed-output keys;
- image/video dispatch boundary.

PostgreSQL-backed `tests/processing_contract.rs` covers:

- load authoritative source row;
- claim kind-0 job;
- verify real source bytes from a disposable media root;
- publish ready media + release post + succeed/clear fenced job;
- reject already-expired publication with no media/release;
- block publication on the job row across lease expiry and verify post-lock expiry rejection.

Existing durable-job tests remain unchanged.

## Correctness/performance decisions

- Keep source verification and codec work outside DB transactions.
- Reuse the verified file handle rather than reopen between verification and processor dispatch.
- Keep full source SHA-256 verification because ingestion records an authoritative digest; measure the cost before considering a combined verify/decode architecture.
- Keep media family sniffing small/bounded; this layer does not attempt full decode validation.
- Keep processor traits explicit; do not couple image/video libraries into durable job/source code.
- Keep processed-output identity versioned and deterministic so at-least-once retries have a stable external side-effect target.
- Fence the final DB publication itself; a lease that was valid before waiting on a row lock is insufficient.
- Do not let publication silently replace a different existing media identity for the same post.
- No Redis is justified.

## Validation status

This branch has **not** been integrated into `v2`.

The current ChatGPT runtime does not provide Rust/Cargo or a real PostgreSQL target environment, so no claim is made that this implementation compiles or passes the DB tests yet. Full target validation is required before integration.

The new `sha2 = "0.10"` dependency must be resolved and checked by Cargo in the validation environment. `src/worker/Cargo.lock` is intentionally ignored by the repository's existing policy.

## Remaining / next-gate observations

- Standard-library canonicalization + symlink rejection protects against DB/path traversal and ordinary symlink substitution, but is not a Linux `openat2`/`O_NOFOLLOW` race-proof sandbox. The media root is service-controlled; flag this only if the target threat model requires protection against a concurrent local attacker with write access to the media tree.
- Full source SHA-256 verification adds one sequential source read before codec consumption. Target-host open/hash/sniff throughput must be measured on local NVMe, including representative large-file sizes.
- Concrete output-file staging/fsync/no-overwrite collision verification belongs with the first codec implementation; the deterministic identity and DB acceptance rules are defined here.
- The ingestion-side source-orphan janitor/reconciliation gap remains open before production ingestion is enabled.
- Deployment still must ensure Go API and Rust worker share the media root with compatible UID/GID/permissions.

## Local-agent evidence workflow

For target-host work, use isolated/disposable resources and return one ZIP with exact SHA/status, commands, raw stdout/stderr, environment/tool versions, measurements, failures, query plans where relevant, and cleanup/restoration evidence. Preserve raw remote evidence until no longer needed.

## Single best next task

Run the execution-only M3 worker processing-contract validation gate against the exact feature-branch SHA after this state-file commit: Rust `cargo fmt --check`, `cargo check`, `cargo clippy --all-targets -- -D warnings`, unit tests, full PostgreSQL-backed worker tests (including the publication lock-wait expiry regression), source-path/symlink/integrity probes, `EXPLAIN (ANALYZE, BUFFERS)` for the fenced publication statement, and target-host benchmarks for source open + SHA-256 + sniff + rewind on representative local-NVMe file sizes. Return one raw-evidence ZIP. If any gate fails, fix this branch before considering fast-forward integration into `v2`.
