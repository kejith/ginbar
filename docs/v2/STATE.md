# Ginbar v2 State / Handoff

Last updated: 2026-10-02
Phase: M3 media pipeline integration — ingestion/enqueue boundary IMPLEMENTED ON BRANCH; validation pending
Integration branch: `v2`
Active M3 branch: `astra/m3-ingestion-enqueue`
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the resume point. Read it before `PLAN.md`. Do not rely on chat history as project memory.

## Branch status

- `master` remains untouched by rewrite work.
- `v2` remains at `3769f4144885d951fd1cddba4c7d1b4d162c5324`.
- M1 board benchmark is complete.
- M2 fresh schema + core Go API is complete and integrated.
- M3 durable PostgreSQL media-job ownership/recovery boundary is complete and integrated.
- The next M3 ingestion/enqueue slice is isolated on `astra/m3-ingestion-enqueue`, branched from exact `v2` tip `3769f4144885d951fd1cddba4c7d1b4d162c5324`.
- Ingestion implementation commits before this state update:
  - `49ee8879f573519c659ff99927d1230a51d71c1f` — initial upload/URL staging + atomic enqueue boundary;
  - `010f105b6b1a073ebe2245ee11b56bc0c9383ec2` — source publication durability + committed 8 MiB staging benchmark;
  - `91b4a3d102f08e6e15f0e60ff08a770d2cbdffc8` — hardened PostgreSQL transaction gate.
- Backend v2 lives under `src/backend/v2`; legacy backend is reference-only.
- Worker v2 lives under `src/worker/v2`; legacy `src/worker` is reference-only unless explicitly reviewed for reuse.
- `.local-agent-results/` is ignored for local-agent evidence ZIPs.

## Retained architecture and validated decisions

Keep unless new evidence contradicts them:

- Go 1.25, pgx/v5, standard `net/http`;
- PostgreSQL authoritative for application/workflow/durable job state;
- no Redis dependency without measured need;
- PostgreSQL pool cap 8;
- request/DB deadline discipline and bounded concurrency;
- post-ID cursor pagination, never OFFSET;
- bounded/indexed hot SQL and target-host measurements before accepting performance-sensitive work;
- immutable numeric relational IDs; usernames never foreign keys;
- content visibility is an allowed-visibility constraint, not merely search context;
- local NVMe is the intended media/source store;
- processing is at-least-once and durable external effects must be idempotent/deterministic.

The integrated media-job boundary retains readiness-first claim ordering, `FOR UPDATE SKIP LOCKED`, lease-generation fencing, post-lock real-time expiry checks, one-job worker concurrency, and no production polling loop/Redis wakeup dependency yet.

## M3 ingestion/enqueue boundary — implemented on branch

### Scope and intentional omissions

This slice creates the source-ingestion/storage/enqueue boundary before codecs.

It deliberately does **not** expose an HTTP posting route yet. Authentication is not implemented in M3 and Ginbar has no anonymous posting, so the service requires an explicit authenticated numeric `Actor.UserID` that a later auth-aware HTTP handler can supply.

It also does not perform media sniffing, decoding, AVIF/video processing, duplicate policy, release, or regeneration. Those belong after the source contract is validated.

### Source schema

Migration `003_media_sources.sql` adds one authoritative source row per post:

- `post_id` primary key / FK to `posts`;
- source type upload or URL;
- unique relative `storage_key`;
- effective source URL for URL imports only;
- optional original upload name;
- declared MIME as untrusted metadata;
- positive byte size;
- SHA-256;
- bounded text lengths and origin consistency checks;
- non-unique SHA-256 index for later exact-duplicate lookup without silently aliasing posts.

The existing `media` table remains the final processed-media record; source metadata is not forced into final width/height/duration fields before processing.

### Staging/storage workflow

`internal/ingest.LocalStore` stages bytes under one configured root:

1. stream to `.staging/` outside a DB transaction;
2. enforce source byte limit using `max + 1` bounded reads;
3. hash SHA-256 while writing;
4. reject empty/oversized/cancelled inputs and remove partial temp files;
5. `fsync` the staged file and set mode `0640`;
6. publish to a random canonical relative key `sources/<shard>/<id>` using a no-overwrite hard link on the same filesystem;
7. sync a newly created shard's parent directory and then the shard directory after publication.

Directories are `0750`. Storage-key removal rejects traversal/noncanonical keys and syncs the containing directory.

Duplicate byte streams intentionally receive distinct random source keys. Exact/perceptual duplicate policy belongs to the processor/release workflow, not staging.

### Service boundary and resource limits

`internal/ingest.Service`:

- requires authenticated positive numeric user ID and valid content filter before staging;
- has explicit staging, DB, and cleanup timeouts;
- bounds total concurrent ingestion workflows with a semaphore;
- performs all byte streaming/downloads outside DB transactions;
- uses a cancellation-independent bounded cleanup context for definite repository failures;
- preserves a staged object on an ambiguous DB commit result, because deleting it could leave a successfully committed PostgreSQL row pointing at a missing file.

A crash after source publication but before/while the DB transaction can still leave an unreferenced source object. This is safe for referenced rows but requires an orphan reconciliation/janitor path before production rollout.

### URL import / SSRF boundary

`HTTPURLFetcher`:

- accepts only HTTP/HTTPS;
- rejects userinfo, fragments, malformed/zero ports, loopback/private/link-local/multicast/unspecified and explicitly blocked special-purpose ranges;
- disables environment HTTP proxies;
- resolves destinations itself and dials the validated resolved IP directly;
- revalidates redirects with a bounded redirect count;
- enforces connect/TLS/response-header timeouts;
- requires HTTP 200;
- rejects declared `Content-Length` over the limit early;
- relies on the local stager's independent byte cap for the authoritative bound, including decompressed/chunked bodies.

### PostgreSQL atomic enqueue

`postgres.Store.CreateIngestion` uses a short transaction and one data-modifying CTE round trip to atomically create:

1. an unreleased post (`release_state = 0`);
2. its `media_sources` row;
3. one initial pending `media_jobs` row (`kind = 0`).

No database transaction spans staging/download. A definite transaction/commit rollback permits source cleanup. A non-rollback commit error is surfaced as `ingest.ErrCommitOutcomeUnknown`, and the source object is preserved for reconciliation.

The PostgreSQL integration test deliberately triggers a later `media_sources` constraint failure after a valid author/post insertion path and verifies `posts`, `media_sources`, and `media_jobs` all remain empty, proving transaction rollback across the authoritative triple rather than only an early FK failure.

## Tests and provisional measurements

Committed unit coverage includes:

- exact bytes/hash/stage/remove;
- oversize and empty input cleanup;
- cancellation cleanup;
- duplicate bytes with distinct keys;
- path traversal/noncanonical key rejection;
- authenticated actor required before staging;
- definite DB/service failure cleanup even after caller cancellation;
- ambiguous commit preserves source;
- effective URL metadata propagation;
- one-slot concurrency bound and cancellation while waiting;
- stage timeout;
- URL syntax/address-range and private-DNS-answer rejection;
- migration-set coverage;
- PostgreSQL success and later-constraint rollback integration cases.

A committed `BenchmarkLocalStoreStage8MiB` measures the durability path (stage + SHA-256 + fsync + publish + remove). A provisional local run in the current ChatGPT execution environment was about 6.6 ms/op / 1.27 GB/s for 8 MiB. This is **not** target-host evidence and must not be used as a production claim.

Pure ingestion-package checks were run provisionally with the locally available Go toolchain and passed `go test` and `go vet`. The current runtime does not provide the required real PostgreSQL/target-host environment, so the full Go 1.25 + PostgreSQL 17 gate remains required.

## Correctness/performance decisions

- Keep file staging outside DB transactions; do not hold a PostgreSQL connection while the client/network streams bytes.
- Keep one SQL round trip inside the short transaction for post/source/job creation.
- Preserve files on ambiguous commit results; prefer an orphan that can be reconciled over a committed DB row whose source was deleted.
- Do not content-address/alias source storage during ingestion; later duplicate processing may make policy decisions with full media context.
- Keep URL SSRF enforcement in the fetcher rather than relying on application DNS or proxy configuration.
- No Redis is justified by this slice.
- No HTTP posting endpoint until authenticated user context exists.

## Unresolved / next-gate observations

- A process crash between source publication and authoritative DB commit can leave an unreferenced file. Add a safe orphan janitor/reconciliation design before production ingestion is enabled; do not solve it with eager deletion that risks referenced-data loss.
- Deployment must ensure the Go API and future Rust worker share the media root with compatible UID/GID/permissions; current file/dir modes are `0640`/`0750`.
- Declared MIME is untrusted metadata; actual type validation/sniffing belongs to worker processing.
- URL imports currently allow any valid public TCP port; revisit only if product/security policy requires a narrower port allowlist.
- No target-host staging throughput or real PostgreSQL transaction-latency measurement exists yet.

## Local-agent evidence workflow

For target-host work, use isolated/disposable resources and return one ZIP with exact SHA/status, commands, raw stdout/stderr, environment/tool versions, measurements, failures, and cleanup/restoration evidence. Preserve raw remote evidence until no longer needed.

## Single best next task

Run the execution-only M3 ingestion/enqueue validation gate against the exact branch SHA after this state-file commit: Go 1.25 format/test/vet/race checks; disposable PostgreSQL 17 migration and atomic success/later-failure rollback tests; filesystem cleanup/cancellation/permission checks; URL SSRF unit checks; target-host 8 MiB staging benchmark; and a small real-PostgreSQL ingestion transaction-latency benchmark. Return one raw-evidence ZIP. If any gate fails, fix this branch before considering fast-forward integration into `v2`.