# Ginbar v2 media worker

This crate now defines the M3 durable job and source-processing boundaries. It still does **not** contain production image/video codecs or a production polling loop.

## Durable job ownership

`media_jobs` ownership remains PostgreSQL-authoritative. Claim/renew/fail/completion use worker identity plus lease generation fencing and strict post-lock expiry checks. Processing occurs outside database transactions and is at-least-once, so durable external effects must be idempotent.

## Source consumption contract

A claimed kind-0 job first loads its authoritative `media_sources` row through the active lease. The worker then verifies the source before dispatch:

- the storage key must exactly match `sources/<2 lowercase hex>/<32 lowercase hex>` and the shard must match the ID prefix;
- the configured media root and `sources` directory are canonicalized once;
- on Unix, shard/file opens use directory-relative `openat` with `O_NOFOLLOW`; traversal and symlink components are not followed;
- source size must match PostgreSQL and remain within the configured bound;
- SHA-256 is recomputed over the full source;
- the first 4 KiB gathered during that same pass is used only to route a supported signature family;
- declared MIME is metadata only and is never trusted for dispatch;
- the verified file handle is rewound and passed to the processor boundary, avoiding a path reopen.

Current signature routing recognizes JPEG, PNG, GIF, WebP, AVIF, common MP4-family brands and WebM. Signature routing is not a substitute for structural decode validation: future codecs must reject malformed/truncated media even if the initial signature is recognized.

Verification classifies failures as retryable I/O, terminal input/integrity errors, or cancellation. The caller supplies the cancellation check; source reads are bounded and cancellation is checked between read operations.

## Processor and output contract

Image and video processors are explicit traits. No codec implementation is selected in this slice.

Before processing, the worker derives a recipe-versioned deterministic output plan from the post ID, verified source SHA-256 and media class. Recipe version 1 uses:

`media/v1/<image|video>/<source-hash-shard>/<post-id>-<source-sha256>`

The key is extensionless; the authoritative output MIME lives in PostgreSQL. A future recipe/codec change that can alter durable bytes or semantics must bump the recipe version rather than overwrite another recipe under the same identity.

A processor must return metadata matching its plan: media class and storage key must match, dimensions and byte size must be positive, duration must be valid, and image duration must be zero.

External output publication is intentionally not implemented yet. Future codecs must write the deterministic key atomically and idempotently (for example temp-file + fsync + no-surprise final publication) before calling the database publication boundary. A retry may recreate/verify the same deterministic output; it must not delete a potentially referenced output merely because commit outcome is ambiguous.

## Fenced database publication

`ProcessingStore::publish_processed` opens one short PostgreSQL transaction **after** source verification/codec work. It:

1. locks the exact currently owned job and post using worker ID + lease generation;
2. locks the authoritative source row and requires its SHA-256 to still equal the verified source plan;
3. rechecks lease expiry with `clock_timestamp()` after lock waits;
4. writes or verifies the exact ready `media` row, releases the non-deleted post, and completes the same fenced job;
5. rolls the whole transaction back if output conflicts, source changed, post is unavailable, or lease completion fails/expired.

The media upsert is intentionally strict: an existing post media row is accepted only when all durable output metadata matches exactly. This avoids silently treating nondeterministic bytes/metadata as idempotent success.

No database transaction spans source hashing or media processing.

## Target measurement probe

`tests/source_verify_bench.rs` contains an ignored target-host probe for the safe-open + full SHA-256 + signature-sniff + rewind path over an 8 MiB source. It creates the fixture outside the timed section.

Example on disposable local storage:

```sh
GINBAR_SOURCE_BENCH_ROOT=/tmp/ginbar-source-bench \
GINBAR_SOURCE_BENCH_ITERATIONS=10 \
cargo test --manifest-path src/worker/v2/Cargo.toml \
  --test source_verify_bench measure_source_open_hash_sniff_8mib \
  -- --ignored --nocapture
```

Target-host measurements are authoritative; do not infer codec throughput from this pre-codec verification probe.
