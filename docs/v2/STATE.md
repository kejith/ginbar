# Ginbar v2 State / Handoff

Last updated: 2026-10-03
Phase: M3 media pipeline integration — ingestion/enqueue boundary FIXED AFTER FAILED GATE; targeted revalidation pending
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
- The M3 ingestion/enqueue slice remains isolated on `astra/m3-ingestion-enqueue`, based on exact `v2` tip `3769f4144885d951fd1cddba4c7d1b4d162c5324`.
- First full ingestion gate tested `ba8c120e5848ec1c887e1dd56cbd6e5fdeff28a3` and failed on one storage-key validation defect; all other requested gates passed.
- Corrective source/test commits after that gate:
  - `77e18a165de765a49da9febcd31c2cd114ea5b46` — enforce exact generated media-source key grammar;
  - `f635d811b7116a39f717bca93fb1ef7402ba20c5` — commit regression coverage for malformed shard/key combinations.
- Backend v2 lives under `src/backend/v2`; legacy backend is reference-only.
- Worker v2 lives under `src/worker/v2`; legacy `src/worker` is reference-only unless explicitly reviewed for reuse.
- `.local-agent-results/` is ignored on the exact feature checkout for evidence ZIPs.

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

## M3 ingestion/enqueue boundary

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

After the first full gate, key validation was tightened so `pathForKey` accepts only the exact generated grammar and additionally requires the two-character shard to equal the first two characters of the 32-hex ID. Existing canonical-path and containment checks remain in place.

### Service boundary and resource limits

`internal/ingest.Service`:

- validates authenticated actor and content filter before staging;
- has explicit staging, DB, and cleanup timeouts;
- bounds total concurrent ingestion workflows with a semaphore;
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

## First full ingestion gate — FAIL on one source defect

Evidence ZIP: `m3-ingestion-enqueue-20261002T224645Z.zip`.
Uploaded ZIP SHA-256: `64F83CD5EBAD0BDD00600256C0E6E8DC27934AAF0EFECCA64F03819FA8722123`.
Exact tested SHA: `ba8c120e5848ec1c887e1dd56cbd6e5fdeff28a3`.
Environment: Ubuntu 24.04.3 target host, Go 1.25.14, PostgreSQL 17.11, Docker 28.5.1, Intel i7-7700, ext4 on `/dev/md2` RAID1 over local Samsung NVMe.

Passed:

- exact SHA/base/clean-checkout provenance and ignored-results-path check;
- required `gofmt` check;
- full Go tests without DB URL;
- `go vet ./...`;
- `go test -race ./internal/ingest`;
- full PostgreSQL-backed `go test ./... -count=1 -v` with both ingestion integration tests executing;
- migration order 001 -> 002 -> 003 and source-origin constraints;
- atomic success: one unreleased post + one source + one pending kind-0 job, no final media row;
- later source-constraint failure rolled back posts/media_sources/media_jobs to zero after a valid post insertion path;
- processing post invisibility from released-feed semantics;
- empty/oversized/cancelled source cleanup;
- exact bytes/hash, duplicate-key isolation, permissions, same-filesystem hard-link publication and directory fsync behavior;
- service authentication/concurrency/timeout/definite-failure cleanup behavior;
- real LocalStore + disposable PostgreSQL failure probe left zero DB rows and no source object;
- ambiguous-commit preservation test;
- URL/SSRF source review and committed tests;
- cleanup/restoration with no gate container/volume/network left and no production state changed.

Only failing requirement:

- `LocalStore.pathForKey` checked canonical path containment but did not enforce the exact generated storage-key grammar. A disposable build-copy regression probe proved these malformed examples were accepted:
  - `sources/a/not-hex`;
  - `sources/aa/not-32-hex`;
  - `sources/ab/0123456789abcdef0123456789abcdef` (shard does not match ID prefix).

This was the sole source-change issue. No PostgreSQL, SSRF, cleanup, transaction, or performance defect was found.

## Target-host baselines from the failed gate

These measurements remain valid because the corrective diff only changes key grammar validation and tests; it does not change DB SQL, file write/fsync/publication operations, hashing, URL fetch, schema, or concurrency behavior.

### Local source staging

`BenchmarkLocalStoreStage8MiB` measures stage + SHA-256 + file fsync + hard-link publication + directory fsync + remove on target local NVMe.

Three runs:

- 47.5255 ms/op, 176.51 MB/s, 67,558 B/op, 32 allocs/op;
- 44.1955 ms/op, 189.81 MB/s, 67,568 B/op, 32 allocs/op;
- 43.7195 ms/op, 191.87 MB/s, 67,568 B/op, 32 allocs/op.

Arithmetic mean: **45.1468 ms/op, 186.06 MB/s, 67,564.7 B/op, 32 allocs/op**.

This is the authoritative first target baseline for the staging durability path, not a speedup claim.

### PostgreSQL ingestion write

PostgreSQL 17.11 disposable benchmark using exact migrations 001/002/003 and the authoritative post/source/job CTE write shape:

- c1: 1,000/1,000, zero failures, **0.348 ms average**, **2,870.87 TPS**;
- c4: 4,000/4,000, zero failures, **0.457 ms average**, **8,761.36 TPS**.

Cumulative state after both runs: 5,000 posts, 5,000 sources, 5,000 jobs, 0 media rows.

This is the first target baseline for this transaction shape.

## Corrective implementation after failed gate

Commits `77e18a165de765a49da9febcd31c2cd114ea5b46` and `f635d811b7116a39f717bca93fb1ef7402ba20c5` change only:

- `src/backend/v2/internal/ingest/local_store.go`;
- `src/backend/v2/internal/ingest/local_store_test.go`.

The validator now requires exactly:

`source/<grammar>` = `sources/<2 lowercase hex>/<32 lowercase hex>`

and the shard must match the first two characters of the 32-character ID.

Committed regression coverage now rejects the three exact failing probe examples plus wrong length, non-hex, uppercase, extra-component, traversal, absolute and wrong-prefix forms. A valid generated-form key is also checked explicitly.

No production PostgreSQL, schema, URL-fetcher, service workflow, hashing, write/fsync/publication, concurrency, or benchmark code changed after the measured gate.

## Remaining non-blocking observations

- Process crash between source publication and authoritative DB commit can leave an unreferenced file. Orphan reconciliation/janitor remains required before production ingestion is enabled.
- Deployment must ensure Go API and future Rust worker share the media root with compatible UID/GID/permissions; current file/dir modes are `0640`/`0750`.
- Declared MIME remains untrusted; worker processing must sniff/validate actual content.
- URL imports currently permit any valid public TCP port; narrow only if product/security policy requires it.
- The target staging and DB transaction baselines above should not be rerun merely for the key-validation fix unless the targeted gate reveals a broader issue.

## Local-agent evidence workflow

For target-host work, use isolated/disposable resources and return one ZIP with exact SHA/status, commands, raw stdout/stderr, environment/tool versions, failures, and cleanup/restoration evidence. Preserve raw remote evidence until no longer needed.

## Single best next task

Run a **targeted execution-only revalidation** against the exact feature-branch SHA after this state-file commit. Verify the diff since `ba8c120e5848ec1c887e1dd56cbd6e5fdeff28a3` changes only `local_store.go`, `local_store_test.go`, and this state document; run Go 1.25 `gofmt`, `go test ./...`, `go vet ./...`, and `go test -race ./internal/ingest`; run the exact malformed-key regression cases plus normal generated-key stage/remove smoke behavior. Do not rerun PostgreSQL/pgbench, URL/SSRF, or the 8 MiB target benchmark unless the targeted checks expose a broader production-code issue. Return one evidence ZIP; if every targeted gate passes, review and fast-forward the ingestion slice into `v2`.
