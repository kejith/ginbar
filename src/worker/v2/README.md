# Ginbar v2 media worker

This crate owns the M3 durable media-job and source-processing boundaries. It still does **not** contain production image/video codecs or a polling loop.

## Durable job ownership

`media_jobs.state` keeps the M2 values: pending `0`, running `1`, succeeded `2`, failed `3`. Each claim increments `attempts` and `lease_generation`; lifecycle mutations require the same worker ID, generation, and an unexpired lease. Row-lock waits are followed by `clock_timestamp()` expiry checks so a worker that crosses expiry while blocked cannot mutate durable state.

Processing happens outside the claim transaction. Durable effects therefore remain at-least-once and must be deterministic/idempotent.

## Source-consumption contract

A claimed kind-0 job first loads its authoritative `media_sources` row through `ProcessingStore::load_source`. The lookup requires the same active worker/generation fence. Declared MIME is retained only as untrusted metadata.

`SourceVerifier` then validates the local shared-root object before any codec receives it:

- source keys must exactly match `sources/<2 lowercase hex>/<32 lowercase hex>` and the shard must match the ID prefix;
- on Unix/Linux, the verifier holds the canonical `sources` directory open and uses `openat` with `O_NOFOLLOW` for both the shard and file component, avoiding path traversal and symlink-following races;
- the configured maximum byte count is enforced before and during reads;
- database byte size and SHA-256 must match the opened object;
- cancellation/deadline checks are caller supplied and evaluated before/open-read work and between bounded reads;
- the same hash pass retains only the first 4 KiB needed for signature sniffing, avoiding a second source scan;
- supported routing signatures are JPEG, PNG, GIF, WebP, AVIF, MP4-family video, and WebM. Unknown signatures are terminal at this boundary.

Signature recognition is a routing/security boundary, not a replacement for codec-level structural validation. Future image/video processors must still parse/decode the complete media before publication.

Published source objects are an application-immutable contract. The verifier carries the same open file handle into processing instead of reopening a path, but a Unix file descriptor is not a snapshot if another trusted process rewrites that inode. Deployment and maintenance code must never mutate a published source in place; replacement/regeneration must create a new durable object/metadata identity.

I/O/open failures are classified retryable, integrity/path/type failures terminal, and caller cancellation separately as `Cancelled`.

## Processor and output contract

`ImageProcessor` and `VideoProcessor` are explicit interfaces. `dispatch_verified_source` routes only from the sniffed type, never from declared MIME.

Every verified source receives a deterministic recipe-v1 primary output key:

`media/v1/<image|video>/<source-hash-shard>/<post-id>-<source-sha256>`

The key is extensionless so codec/container choices can evolve only by bumping the processing recipe version rather than relying on filename semantics. `ProcessedMedia` must match its output plan and supply positive dimensions/size, non-negative duration, output SHA-256, MIME, and optional perceptual hash.

No encoder is selected or optimized in this slice.

## Fenced publication transaction

`ProcessingStore::publish_processed` is the only durable publication boundary. It opens a short PostgreSQL transaction after processing is complete and:

1. in one preflight SQL round trip, locks the exact owned job/post plus authoritative source row and evaluates real-time lease validity after those locks;
2. requires the source SHA-256 to still match the verified processing plan;
3. idempotently inserts/updates the exact same `media` result, marks it ready, releases the post, and completes the job;
4. rechecks the worker/generation/expiry fence in the final job update;
5. rolls the whole transaction back if output conflicts, source metadata changed, the post is unavailable, or the lease expires before completion.

The output upsert only accepts an already-existing row when all durable output metadata matches exactly. This prevents a retry from silently blessing nondeterministic output under the same deterministic key.

A commit error may be ambiguous. The media file therefore must be published under its deterministic output key before this database transaction; a later retry can safely recreate/verify the same output instead of deleting an object that may already be referenced by a committed transaction.

## Concurrency and probes

Worker execution remains one job at a time and there is still no production polling loop or Redis dependency. `claim-once` remains the crash/reclaim probe.

The ignored `measure_source_open_hash_sniff_8mib` integration test is a target-host measurement probe. It creates one disposable 8 MiB JPEG-signature source outside the timed region, then measures repeated safe-open + full SHA-256 + sniff + rewind work. Set `GINBAR_SOURCE_BENCH_ROOT` to a disposable directory on the target local media filesystem before running it.
