# Ginbar v2 media worker

The v2 Rust worker owns durable media-job execution. M3 includes durable lease/fencing, source verification, deterministic durable output publication, the still-image processor, and the accepted long-running PostgreSQL polling runner with lease renewal, bounded retries and graceful shutdown. Video processing is the next media slice and remains out of scope for processing version 1.

## Durable job ownership

`media_jobs.state` uses `0=pending`, `1=running`, `2=succeeded`, `3=failed`. Each successful claim increments both `attempts` and `lease_generation`. Completion, failure, renewal and processed-media publication require the same worker ID, lease generation and an unexpired lease. Lifecycle mutations lock the exact owned row before checking expiry with `clock_timestamp()`.

Processing never runs inside the claim transaction. Accepted still-image concurrency is one job at a time and there is no Redis dependency. The long-running runner polls PostgreSQL when idle and renews the active job lease on a separate PostgreSQL connection while processing is in progress.

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

Integrity/type/key failures are terminal. Database/filesystem I/O and cancellation are retryable. The dispatch sniffer recognizes JPEG, PNG, GIF, WebP, AVIF/HEIF-family ISO-BMFF images, MP4-family video and EBML-family video. Family recognition does not imply that processing version 1 implements every decoder.

## Deterministic output identity

`processed_output_key` produces the canonical versioned identity:

`media/<post shard>/<post id>/v<processing version>-<source SHA-256>.<format>`

The identity includes immutable post ID, authoritative source digest, processing-contract version and output format. An output-affecting algorithm, codec configuration or format change must increment `PROCESSING_VERSION` rather than silently reuse an incompatible key.

The canonical image is the AVIF recorded in `media.storage_key`. Its auxiliary square thumbnail is derived from the same identity by inserting `.thumb` before `.avif`; it is not a second `media` row.

## Durable no-overwrite filesystem publication

`output::OutputStore` publishes processed files before database publication:

1. validate a relative `media/...` key and reject symlink/non-directory path components;
2. create a unique same-directory staging file with `create_new`;
3. write the complete encoded object and `fsync` the staging file;
4. create the final name with a same-filesystem hard link, which never replaces an existing destination;
5. `fsync` the containing directory;
6. remove the staging name and `fsync` the directory again.

If the final name already exists, the worker streams it, verifies exact byte length and SHA-256, and only then treats it as an idempotent retry. Different bytes at the deterministic key are a terminal collision and are never overwritten. A crash after the final directory sync is safe: the final object is durable and a retry reuses it after verification. A crash before final publication can leave only a hidden staging file; stale-stage cleanup is a separate housekeeping concern and is not needed for correctness.

The target deployment is local ext4/NVMe, where same-directory hard links provide the required atomic no-overwrite primitive. Revalidate this assumption if the media store changes filesystem semantics.

## Bounded still-image processor

`image::BoundedImageProcessor` accepts:

- JPEG;
- non-animated PNG;
- non-animated WebP.

Animated PNG/WebP, GIF, AVIF/HEIF input and all video are terminal for processing version 1 rather than being silently flattened or partially decoded.

Before any durable output is created, the processor fully decodes the source, applies decoder orientation, validates post-orientation dimensions, constructs both outputs and encodes both AVIF byte buffers. Current constants in `src/image.rs` are part of the accepted output contract: maximum input dimension/pixel/allocation limits, 1280 px maximum main dimension, 256 px square thumbnail, AVIF quality settings, speed and one encoder thread.

`image` is built with default features disabled and only JPEG/PNG/WebP enabled. `ravif` is configured at runtime with one encoder thread so one image job cannot consume the host-wide Rayon pool. Accepted still-image worker concurrency remains one job.

The processor publishes the thumbnail first and the canonical AVIF second. Only after both are durable does it return `ProcessedMedia`; `publication::publish_processed` is then the database commit point.

## Atomic processed-media publication

`publication::publish_processed` receives the digest of the source that was actually verified. One PostgreSQL statement:

1. identifies the exact running kind-0 job by ID, post ID, worker ID and lease generation;
2. requires the current authoritative `media_sources.sha256` to match the verified source digest and requires the deterministic output key to embed that digest;
3. locks the job and post and rechecks lease expiry using post-lock real time;
4. writes ready `media` (`processing_state=1`) or accepts an idempotent existing row only when storage key and output SHA-256 match;
5. releases the post only after the ready media row exists;
6. succeeds the fenced job and clears ownership only after those mutations succeed.

An expired/stale lease, source-identity change, deleted/non-releasable post or conflicting prior media identity returns `LeaseLostOrConflict` without releasing the post or completing the job. No PostgreSQL transaction spans source hashing, decode/encode or filesystem output work.

## Production polling runner

`run` starts the accepted one-job-at-a-time production runner. It claims one durable job, starts an independent lease heartbeat, processes the job, records the fenced outcome and immediately claims again after successful work. When no job is available it sleeps for a bounded poll interval instead of busy-spinning.

The heartbeat uses a separate PostgreSQL connection and renews at roughly one third of the configured lease duration. A lost lease generation cancels the active job boundary and prevents stale database publication. Repeated heartbeat connection/query failures are classified separately and become retryable job failures when ownership can still be proven. SIGINT/SIGTERM stop new claims; active processing observes shutdown at cancellation boundaries and attempts to requeue the owned job immediately.

Production PostgreSQL connections derive an operation budget from the configured lease: `min(lease / 10, 3 seconds)`, with a 1 ms floor. That value is applied as socket-level `connect_timeout` and PostgreSQL `statement_timeout` for every worker connection, including heartbeat connections and one-shot probes. Existing PostgreSQL `options` are preserved and the worker timeout is appended. The connect timeout applies per socket-level address attempt; the target architecture uses local PostgreSQL, so multi-host failover timing is not part of the current production contract.

AVIF encoding and durable filesystem publication are synchronous calls in processing version 1. They cannot be interrupted in the middle of the call. Cancellation is checked again before the authoritative fenced database commit, so stale work cannot release the post or complete the job; deterministic no-overwrite files created before cancellation remain safe for retry.

```sh
DATABASE_URL='postgres://...' \
GINBAR_MEDIA_ROOT='/path/to/media-root' \
GINBAR_WORKER_ID='media-worker-1' \
GINBAR_WORKER_LEASE_MS=30000 \
cargo run --release --locked --manifest-path src/worker/v2/Cargo.toml -- run
```

Runner tuning variables are optional:

- `GINBAR_WORKER_IDLE_POLL_MS` defaults to 500 ms;
- `GINBAR_WORKER_DB_RETRY_BASE_MS` defaults to 250 ms;
- `GINBAR_WORKER_DB_RETRY_MAX_MS` defaults to 5,000 ms;
- `GINBAR_WORKER_RENEW_RETRY_BASE_MS` defaults to 100 ms;
- `GINBAR_WORKER_RENEW_RETRY_MAX_MS` defaults to 1,000 ms;
- `GINBAR_WORKER_RENEW_FAILURE_LIMIT` defaults to 3;
- `GINBAR_WORKER_MAX_SOURCE_BYTES` defaults to 512 MiB.

Keep the accepted one-job/one-encoder-thread still-image concurrency until new measurements justify a change.

## One-shot processing probes

`process-once` claims and processes at most one still-image job end to end. It does not renew the lease and remains useful for isolated processing probes.

```sh
DATABASE_URL='postgres://...' \
GINBAR_MEDIA_ROOT='/path/to/disposable/media-root' \
GINBAR_WORKER_ID='m3-image-probe-1' \
GINBAR_WORKER_LEASE_MS=300000 \
cargo run --release --locked --manifest-path src/worker/v2/Cargo.toml -- process-once
```

Use a lease comfortably longer than the measured single-image processing time for `process-once`. `claim-once` remains the lease crash/recovery probe: it claims at most one job and exits without completion so lease reclaim can be tested.

## Target-host evidence

`examples/image_bench.rs` runs the verified-source + decode/transform/AVIF/durable-publication path while timing source verification separately from processing. Each iteration uses a fresh post ID so it cannot benchmark deterministic output reuse by accident.

```sh
cargo run --release --locked --manifest-path src/worker/v2/Cargo.toml \
  --example image_bench -- /path/to/real-image.jpg 5
```

The production runner was accepted on the target host at exact revision `534f9c3add3b4bec4cab405544f7741fa11d1b30`. The retained evidence bundle is `m3-runner-20261004T001829Z.zip`, SHA-256 `f709ebc3c097e1d4fb88903cbda1dcdb6c90babda4df2ce1058226a66bdd57a3`.

Key accepted observations:

- idle worker median CPU 0%, peak 0.91%, RSS 3,712 KiB;
- 1 s forced lease renewed repeatedly during a 2.322 s 8192x8192 job without ownership/generation loss;
- 2,000-job continuous drain: 2,000 succeeded, zero failed/stale jobs, maximum one running, 1.505 jobs/s;
- active worker CPU median 93.69%, peak 99.01%; RSS median 75,944 KiB, peak 130,332 KiB;
- shutdown during long encode requeued safely without authoritative publication; restart completed on the next generation;
- 80,000/80,000 measured API requests succeeded during baseline/continuous-runner coexistence testing;
- at concurrency 8, same-run loaded p95 was 4.692 ms versus 3.947 ms baseline (+18.88%), and throughput was 2,406.90 versus 2,667.60 req/s (-9.77%).

These measurements justify the simple PostgreSQL polling/heartbeat architecture and the one-job/one-encoder-thread image limit. They do not justify Redis wakeups, scheduler quotas, cgroups, core pinning or load admission.

Stable benchmark details live in `docs/v2/PERFORMANCE.md`.

## Next media boundary

Video must be designed and measured independently rather than inheriting still-image codec assumptions. The next slice should keep the existing verified-source, deterministic-output, fenced-publication and runner ownership contracts, choose a proven bounded video toolchain, make codec/thread limits explicit, and require a target-host video resource/API-interference gate before acceptance.

Target-host execution remains read-only/execution-only by default: do not modify tracked source, SQL, docs, config, commits, branches, deployments or persistent application state. Preserve raw evidence and return one bundle under `.local-agent-results/` for primary-session analysis.
