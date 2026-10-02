# Ginbar v2 media worker

The v2 Rust worker owns durable media-job execution. M3 now defines the durable lease/fencing boundary plus the source-consumption and processed-media publication contract. Real AVIF/video codec implementations are deliberately not part of this slice.

## Durable job ownership

`media_jobs.state` uses `0=pending`, `1=running`, `2=succeeded`, `3=failed`. Each successful claim increments both `attempts` and `lease_generation`. Completion, failure, renewal, and processed-media publication require the same worker ID, lease generation, and an unexpired lease. Lifecycle mutations lock the exact owned row before checking expiry with `clock_timestamp()`.

Processing never runs inside the claim transaction. Initial production concurrency remains one job at a time and there is still no polling loop or Redis dependency.

## Source-consumption contract

A claimed kind-0 job is prepared with `processing::prepare_claimed_source`:

1. load authoritative `media_sources` by immutable `post_id`;
2. reject non-kind-0 jobs and missing/corrupt source metadata;
3. validate the exact `sources/<2 lowercase hex>/<32 lowercase hex>` ingestion key grammar;
4. resolve inside the configured shared root and reject symlink source/shard entries;
5. enforce a caller-supplied maximum source size;
6. compare filesystem and DB byte sizes;
7. stream with a fixed-size buffer while checking cancellation and SHA-256;
8. sniff actual media bytes rather than trusting `declared_mime_type`;
9. rewind the same verified file handle before dispatch.

Integrity/type/key failures are terminal. Database/filesystem I/O and cancellation are retryable. The dispatch sniffer currently recognizes JPEG, PNG, GIF, WebP, AVIF/HEIF-family ISO-BMFF images, MP4-family video, and EBML-family video. This is a family-routing boundary, not full decoder validation.

## Processor and output identity boundaries

`ImageProcessor` and `VideoProcessor` are explicit traits. No concrete codec exists yet.

`processed_output_key` produces a deterministic, versioned identity:

`media/<post shard>/<post id>/v<processing version>-<source SHA-256>.<format>`

The identity includes immutable post ID, authoritative source digest, processing-contract version, and output format. An incompatible processing algorithm must increment the version rather than silently reuse old output keys.

Future processors must durably publish output under this deterministic identity before returning `ProcessedMedia`. Output writes must be no-overwrite/idempotent: retries may reuse an existing deterministic object only after proving it is the same result and must never replace unrelated bytes in place.

## Atomic processed-media publication

`publication::publish_processed` is the short authoritative commit point after codec/output-file work. It receives the digest of the source that was actually verified. One PostgreSQL statement:

1. identifies the exact running kind-0 job by ID, post ID, worker ID and lease generation;
2. requires the current authoritative `media_sources.sha256` to match the verified source digest and requires the deterministic output key to embed that digest;
3. locks the job and rechecks lease expiry using post-lock real time;
4. writes ready `media` (`processing_state=1`) or accepts an idempotent existing row only when storage key and output SHA-256 match;
5. releases the post only after the ready media row exists;
6. succeeds the fenced job and clears ownership only after those mutations succeed.

An expired/stale lease, source-identity change, deleted/non-releasable post, or conflicting prior media identity returns `LeaseLostOrConflict` without releasing the post or completing the job. No PostgreSQL transaction spans source hashing, decode/encode, or filesystem output work.

## Crash/idempotency notes

At-least-once execution still permits crashes around filesystem side effects, so deterministic keys and no-overwrite output publication remain mandatory even though the DB commit is fenced. The ingestion-side source-orphan reconciliation requirement also remains open before production ingestion is enabled.

## Boundary probe

`claim-once` remains a crash/recovery probe: it claims at most one job and exits without completion so lease reclaim can be tested.

```sh
DATABASE_URL='postgres://...' \
GINBAR_WORKER_ID='m3-probe-1' \
GINBAR_WORKER_LEASE_MS=1000 \
cargo run --manifest-path src/worker/v2/Cargo.toml -- claim-once
```
