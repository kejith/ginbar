# Ginbar v2 State / Handoff

Last updated: 2026-10-03
Phase: M3 media pipeline integration — ingestion/enqueue boundary PASSED AND INTEGRATED
Integration branch: `v2`
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the resume point. Read it before `PLAN.md`. Do not rely on chat history as project memory.

## Branch status

- `master` remains untouched by rewrite work at `181fa44d79c7b4a1984c1a35795762dd503b3f77`.
- M1 board benchmark is complete.
- M2 fresh schema + core Go API is complete and integrated.
- M3 durable PostgreSQL media-job ownership/recovery boundary is complete and integrated.
- M3 upload/URL-ingestion + durable-enqueue boundary is complete and integrated.
- `v2` was fast-forwarded from `3769f4144885d951fd1cddba4c7d1b4d162c5324` through the exact validated ingestion SHA `01cd1887fbb42cdcd0f3881dd38395495dc5047b`, then received this state-only integration commit.
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

The integrated media-job boundary retains readiness-first claim ordering, `FOR UPDATE SKIP LOCKED`, lease-generation fencing, post-lock real-time expiry checks, one-job worker concurrency, and no Redis wakeup dependency.

## M3 ingestion/enqueue boundary — integrated

### Scope

This slice establishes source ingestion/storage plus durable enqueue before codecs.

It deliberately does **not** expose an HTTP posting route. Authentication is not implemented in M3 and Ginbar has no anonymous posting; callers must supply a positive authenticated numeric `Actor.UserID`.

It also does not perform media sniffing, decoding, AVIF/video processing, duplicate policy, release, regeneration, or progress UI.

### Source schema

Migration `003_media_sources.sql` adds one authoritative source row per post:

- `post_id` primary key / FK to `posts`;
- upload or URL source type;
- unique relative source `storage_key`;
- effective source URL for URL imports only;
- optional original upload name;
- declared MIME as untrusted metadata;
- positive byte size;
- SHA-256;
- bounded text/origin constraints;
- non-unique SHA-256 index for future exact-duplicate lookup without aliasing posts.

The existing `media` table remains processed output; source metadata is not forced into final width/height/duration fields before processing.

### Staging/storage workflow

`internal/ingest.LocalStore`:

1. streams to `.staging/` outside a DB transaction;
2. enforces `maxBytes + 1` bounded reads;
3. hashes SHA-256 while writing;
4. rejects empty/oversized/cancelled inputs and removes partial temp files;
5. `fsync`s the staged file and uses mode `0640`;
6. publishes to a random `sources/<2-lower-hex>/<32-lower-hex>` key with no-overwrite hard-link publication on the same filesystem;
7. syncs newly created shard-parent metadata and the shard directory.

Directories use `0750`. Duplicate byte streams intentionally get distinct keys; exact/perceptual duplicate policy belongs later in processing/release.

Storage-key removal accepts only the exact generated grammar `sources/<2 lowercase hex>/<32 lowercase hex>`, requires the shard to equal the first two ID characters, and retains canonical-path/containment checks as defense in depth.

### Service boundary and resource limits

`internal/ingest.Service`:

- validates authenticated actor and content filter before staging;
- has explicit staging, DB, and cleanup timeouts;
- bounds concurrent ingestion workflows with a semaphore;
- performs byte streaming/download outside DB transactions;
- uses cancellation-independent bounded cleanup after definite repository failure;
- preserves a staged object on ambiguous DB commit outcome, because deleting it could leave committed PostgreSQL state pointing at missing bytes.

A process crash after source publication but before/while authoritative DB commit can still leave an unreferenced source object. This is safe for referenced data but requires an orphan-reconciliation/janitor path before production ingestion is enabled.

### URL import / SSRF boundary

`HTTPURLFetcher`:

- accepts only HTTP/HTTPS;
- rejects userinfo, fragments, malformed/zero ports, loopback/private/link-local/multicast/unspecified and explicitly blocked special-purpose ranges;
- disables environment HTTP proxies;
- resolves destinations itself and dials only validated resolved IPs;
- revalidates redirects with a bounded redirect count;
- has explicit connect/TLS/response-header timeouts;
- requires HTTP 200;
- rejects oversized declared `Content-Length` early;
- relies on LocalStore's independent stream bound for the authoritative decompressed/chunked byte cap.

### PostgreSQL atomic enqueue

`postgres.Store.CreateIngestion` uses a short transaction and one data-modifying CTE round trip to atomically create:

1. an unreleased post (`release_state = 0`);
2. its `media_sources` row;
3. one initial pending kind-0 `media_jobs` row.

No DB transaction spans staging/download. Definite rollback permits source cleanup. Non-rollback commit errors surface as `ingest.ErrCommitOutcomeUnknown`; the source object is retained for reconciliation.

## Ingestion validation history

### First full gate — FAIL on one source-key validator defect

Evidence ZIP: `m3-ingestion-enqueue-20261002T224645Z.zip`.
ZIP SHA-256: `64F83CD5EBAD0BDD00600256C0E6E8DC27934AAF0EFECCA64F03819FA8722123`.
Exact tested SHA: `ba8c120e5848ec1c887e1dd56cbd6e5fdeff28a3`.
Environment: Ubuntu 24.04.3 target host, Go 1.25.14, PostgreSQL 17.11, Docker 28.5.1, Intel i7-7700, ext4 on `/dev/md2` RAID1 over local Samsung NVMe.

Passed in that full gate:

- provenance, `gofmt`, full Go tests, vet and ingest race tests;
- migration order 001 -> 002 -> 003;
- real PostgreSQL atomic success and later-constraint rollback tests;
- unreleased-post invisibility;
- staging size/cancel/cleanup/permissions/durability checks;
- service authentication/concurrency/timeout/cleanup semantics;
- ambiguous-commit preservation;
- URL/SSRF boundary checks;
- cleanup/restoration;
- target staging and PostgreSQL latency benchmarks.

The only failure was that `LocalStore.pathForKey` accepted malformed but contained paths. Corrective commits `77e18a165de765a49da9febcd31c2cd114ea5b46` and `f635d811b7116a39f717bca93fb1ef7402ba20c5` tightened the grammar and added regression coverage. No PostgreSQL/schema/URL/service/write/fsync/concurrency/benchmark code changed.

### Final targeted gate — PASS

Evidence ZIP: `m3-ingestion-enqueue-final-20261002T225737Z.zip`.
ZIP SHA-256: `538F65E7C196BB24FF8C4FBB0F155221DB40E38BD78C2B79A794CFD7DA1CEF0F`.
Exact tested SHA: `01cd1887fbb42cdcd0f3881dd38395495dc5047b`.

Verified from raw evidence:

- exact feature SHA and exact merge-base `3769f4144885d951fd1cddba4c7d1b4d162c5324`;
- detached tested checkout was clean and `.local-agent-results/probe.zip` was ignored;
- diff since the first full gate changed only `local_store.go`, `local_store_test.go`, and `docs/v2/STATE.md`;
- `gofmt`, `go test ./...`, `go vet ./...`, and `go test -race ./internal/ingest` all passed;
- malformed-key regression tests each passed 10/10;
- generated-key Stage -> validation -> Remove passed 10/10;
- duplicate-byte distinct-key smoke passed 10/10;
- no targeted test skipped or flaked;
- an initial malformed-key command had a shell-quoting error before any test ran; the corrected command was captured separately and passed, so this was not a product/test failure;
- the two unchanged PostgreSQL tests skipped only in the explicit no-DB check because `GINBAR_TEST_DATABASE_URL` was intentionally unset;
- disposable runner/checkout were removed and refs/persistent state were unchanged during the gate.

Decision: the M3 ingestion/enqueue boundary PASSES and is integrated into `v2`.

## Accepted target-host baselines

These remain the authoritative first baselines for this slice because the final correction did not change the measured paths.

### Local source staging

`BenchmarkLocalStoreStage8MiB` includes stage + SHA-256 + file fsync + hard-link publication + directory fsync + remove on target local NVMe.

Three runs:

- 47.5255 ms/op, 176.51 MB/s, 67,558 B/op, 32 allocs/op;
- 44.1955 ms/op, 189.81 MB/s, 67,568 B/op, 32 allocs/op;
- 43.7195 ms/op, 191.87 MB/s, 67,568 B/op, 32 allocs/op.

Arithmetic mean: **45.1468 ms/op, 186.06 MB/s, 67,564.7 B/op, 32 allocs/op**.

### PostgreSQL ingestion write

PostgreSQL 17.11, exact migrations 001/002/003 and authoritative post/source/job CTE write shape:

- c1: 1,000/1,000, zero failures, **0.348 ms average**, **2,870.87 TPS**;
- c4: 4,000/4,000, zero failures, **0.457 ms average**, **8,761.36 TPS**.

Cumulative disposable benchmark state after both runs: 5,000 posts, 5,000 sources, 5,000 jobs, 0 media rows.

## Remaining non-blocking observations

- Process crash between source publication and authoritative DB commit can leave an unreferenced file. Orphan reconciliation/janitor remains required before production ingestion is enabled.
- Deployment must ensure the Go API and Rust worker share the media root with compatible UID/GID/permissions (`0640` files / `0750` directories currently).
- Declared MIME is untrusted; worker processing must sniff/validate actual content.
- URL imports currently permit any valid public TCP port; narrow only if product/security policy requires it.
- The target staging and ingestion-write baselines should only be rerun when the measured paths materially change.

## Local-agent evidence workflow

For target-host work, use isolated/disposable resources and return one ZIP with exact SHA/status, commands, raw stdout/stderr, environment/tool versions, measurements, failures, and cleanup/restoration evidence. Preserve raw remote evidence until no longer needed.

## Single best next task

Start the next M3 slice from current `v2`: implement the Rust worker source-consumption/processing contract before codec optimization. A claimed kind-0 job must load its authoritative `media_sources` row, open the canonical shared-root source safely, verify byte-size/SHA-256 consistency, sniff and validate the real media type instead of trusting declared MIME, and dispatch through explicit image/video processor boundaries. Define deterministic/idempotent processed-output keys and the short PostgreSQL publication transaction that will eventually write `media`, release the post, and complete the fenced job, but do not yet optimize AVIF/video encoding. Add bounded I/O/cancellation/error classification and target-host measurements for source-open/hash/sniff overhead so the following codec slice starts from a validated execution contract.
