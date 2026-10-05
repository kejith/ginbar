# Ginbar v2 state / handoff

Last updated: 2026-10-05
Phase: **M4 connected core product in progress; auth/session candidate CI-green, target auth gate pending**
Integration branch: `v2`
Legacy branch: `master` (read-only for rewrite work)

Read this file first. Use [`PLAN.md`](PLAN.md) for stable milestone/product rules and [`PERFORMANCE.md`](PERFORMANCE.md) for accepted benchmark history. Do not use chat history as project memory.

## Current rewrite status

- M1 board performance prototype: **complete and integrated**.
- M2 fresh PostgreSQL schema + core Go API: **complete and integrated**.
- M3 media pipeline: **complete for the accepted v2 scope and integrated**.
- M4 connected core product: **in progress; authentication/session foundation implemented on a feature branch and awaiting target-host KDF/query-plan acceptance evidence**.

M3 accepted scope now includes:

- durable PostgreSQL media jobs with transactional claiming, retry/recovery, leases and generation fencing;
- upload/URL ingestion and durable source verification;
- deterministic generation-fenced DB publication and atomic no-overwrite local-NVMe publication;
- JPEG/PNG/WebP -> deterministic AVIF canonical + 256x256 AVIF thumbnail processing;
- production Rust worker runner with renewal, cancellation, reconnect/backoff, graceful shutdown and crash/restart recovery;
- compatible MP4 H.264/AAC zero-transcode canonical publication plus deterministic AVIF thumbnail;
- exact perceptual-dHash duplicate candidates with bounded PostgreSQL lookup;
- durable regeneration through the same processing/job/publication path while preserving last-known-good media;
- authoritative coarse media progress/status exposure plus bounded selected-post polling UI.

Broader video transcoding, exact WebM/EBML acceptance and operational orphan/janitor hardening remain deferred until concrete product/production requirements justify them; they do not block the accepted M3 gate.

## M4 authentication/session foundation — candidate awaiting target gate

Feature branch: `astra/m4-auth-session`, branched from `v2` at `24aba0cda53641f7ef1cf107bbe7611b1803fca6`.

CI-green executable candidate:

`33d72e7b5d56fe9bf8608b305984d4e150646fd5`

Relevant commits:

- `fd72a4069859dda718d677c080dc67ff4677cf0c` — invitation registration, Argon2id password credentials, opaque PostgreSQL sessions, auth HTTP boundary, migration `006_auth_sessions.sql`, focused service/store/HTTP/security/concurrency tests and deterministic KDF benchmarks;
- `33d72e7b5d56fe9bf8608b305984d4e150646fd5` — align the Go workspace dependency graph with `x/text`'s required `x/sync v0.22.0` so readonly CI does not attempt to create `go.work.sum`.

Applicable CI:

- run `37256793597` on `fd72a4069859dda718d677c080dc67ff4677cf0c`: **failed before Go tests** because the readonly workspace needed the selected `golang.org/x/sync v0.22.0` checksum; Rust regression coverage had already passed and the tracked checkout remained clean;
- run `37256998256` on `33d72e7b5d56fe9bf8608b305984d4e150646fd5`: **success**, including scoped Go 1.25 formatting/vet/PostgreSQL-backed correctness, migration-triggered Rust regression checks, hermetic target-worker release build and clean tracked checkout.

Implemented contract:

- immutable numeric `users.id` remains relational identity; username is not an authorization/foreign-key identity;
- existing credential kind `0` is used as the initial password credential slot; existing user status `0` is treated as active/default according to the M2 schema semantics; `user_identities` remains independent for later OIDC/OAuth/passkey/email identities;
- no baseline ordinary-user role is created because current schema/product semantics do not require one;
- passwords use `golang.org/x/crypto/argon2` Argon2id with a self-describing verifier containing algorithm/version/parameters/salt/hash; password input is bounded to 12-1024 bytes before KDF work and stored verifier parameters are bounded before allocation/work;
- current provisional Argon2id default is 64 MiB memory, one iteration, parallelism one, 16-byte random salt and 32-byte output; this is **not accepted until target-host measurement**;
- registration validates cheap request bounds, hashes the invitation token with SHA-256, performs an indexed availability precheck, hashes the password outside the transaction, then transactionally locks/revalidates the invitation with `FOR UPDATE`, creates the user and kind-0 credential, claims the invitation by numeric user ID and commits atomically;
- raw passwords, invitation tokens and session tokens are never persisted by the implemented boundaries; stable API errors do not echo supplied secrets or stored verifiers;
- concurrent claims of one invitation have at most one winner; concurrent case-insensitive duplicate usernames have at most one winner; credential-creation failure rolls back both the user and invitation claim;
- sessions use 32 cryptographically random bytes encoded as an opaque base64url cookie token; PostgreSQL stores only SHA-256 of the raw token, numeric `user_id`, creation, absolute expiry and optional revocation;
- session expiry is fixed at 30 days initially; authenticated lookup is read-only and performs no last-seen/sliding-expiry write; multiple independent sessions are allowed;
- migration `006_auth_sessions.sql` adds `user_sessions` with a unique 32-byte token-hash index; no speculative user/session indexes or Redis cache were added;
- session resolution is one bounded token-hash lookup joined to the minimal active user fields; logout authoritatively marks the presented session revoked and is idempotent for absent/malformed/already-revoked sessions;
- browser endpoints are `POST /api/v2/auth/register`, `POST /api/v2/auth/login`, `POST /api/v2/auth/logout` and authenticated `GET /api/v2/auth/me`; the current-user response contains only numeric ID and username;
- reusable HTTP auth middleware performs one session resolution and stores an immutable numeric principal in request context; resource-specific authorization remains a domain-service concern;
- production cookies are `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/` with explicit expiry/max-age; `GINBAR_SESSION_COOKIE_SECURE` is a development/test-only practical override while secure remains the default;
- auth POSTs require same-origin `Origin` metadata; reverse-proxy scheme is derived from nginx's existing `X-Forwarded-Proto` boundary; SameSite remains defense in depth;
- auth JSON request bodies are capped at 4096 bytes and reject unknown fields before expensive work;
- nonexistent usernames return the same public invalid-credentials shape but intentionally do not run a dummy Argon2 hash in this slice, avoiding an attacker-controlled KDF path before rate-limit/abuse policy is designed and measured;
- existing unauthenticated feed/media-status read behavior remains unchanged; this slice does not connect board mutations, uploads, votes, tags, comments, full profiles, moderation/admin, private messages, Redis or JWT infrastructure.

Focused tests cover password verifier round-trip/random salt/malformed verifier/input bounds; invitation state and rollback; case-insensitive duplicate and same-invitation concurrency; session token hashing, independent sessions, expiry, revocation, user status and non-mutating lookup; cookie attributes; origin/CSRF policy; bounded HTTP bodies; stable unauthorized semantics; and secret non-echo behavior. Existing M1-M3 applicable regression checks remain green in CI.

Prepared deterministic target KDF benchmarks cover hash cost, verify cost, parallel verification pressure and parameter candidates `32MiB-t2`, `64MiB-t1`, `64MiB-t2`, and `128MiB-t1`.

### Pending target acceptance gate

No target-host KDF benchmark or realistic auth SQL query-plan evidence has been accepted yet. The target gate is pinned to exact executable candidate `33d72e7b5d56fe9bf8608b305984d4e150646fd5`, which has green CI run `37256998256`. Later state-only documentation commits are not substitute executable candidates. This gate is required before integration because Argon2 cost is hardware-sensitive and session lookup will become request-path hot.

Target evidence must measure exact executable candidate `33d72e7b5d56fe9bf8608b305984d4e150646fd5`, including:

- hash/registration and verify/login Argon2 cost, CPU behavior, peak/representative memory, and parallel verification pressure on the shared i7-7700-class target;
- `EXPLAIN (ANALYZE, BUFFERS)` for production-shaped session token-hash resolution, username/password-credential lookup and invitation token lookup/lock against realistic row counts;
- evidence that the unique session token-hash index, existing case-insensitive username index and existing invitation token-hash index bound the intended hot paths without request-path writes or sequential scans.

Do not weaken KDF parameters merely for throughput. If target measurements justify changing the provisional default, change it here, rerun applicable CI and repeat the exact target measurement before acceptance. No broader API coexistence benchmark is required unless the KDF measurements show material interactive concurrency risk.

## M3 progress/status — accepted and integrated

Feature branch: `astra/m3-progress-status`, branched from `v2` at `4e547d1d06f862950ff820897b12e401291c0bec`.

Exact executable candidate target-gated:

`ba3265910ed7ede38e08e925a7cbdc0b63fdc9ef`

Integrated feature/docs head before this state-only commit:

`cf3f3d40f573e58dd6feb7cbd74136059a531e0f`

Relevant feature commits:

- `640171e2e07a42257a851d2fb844049d1157e81c` — authoritative media-status domain/store/API boundary and reusable Solid status/polling UI;
- `d3552bcec0fd3bc99a3373f81f26b308e50a0db9` — service formatting correction;
- `9a8903f34c39d1a5ccaae4addd1ca3494c752df6` — status-test formatting correction;
- `ba3265910ed7ede38e08e925a7cbdc0b63fdc9ef` — bounded retry-polling frontend coverage;
- `cf3f3d40f573e58dd6feb7cbd74136059a531e0f` — candidate state documentation only; production code unchanged from `ba326591...`.

Applicable CI:

- backend run `37248297712`: **success** on `9a8903f34c39d1a5ccaae4addd1ca3494c752df6`, including Go formatting/vet, PostgreSQL-backed tests, applicable hermetic worker-build check and clean tracked checkout;
- exact candidate run `37248392896`: **success** on `ba3265910ed7ede38e08e925a7cbdc0b63fdc9ef`, including all frontend tests, TypeScript check, Vite production build and clean tracked checkout;
- post-fast-forward `v2` run `37250108850`: **success** on `cf3f3d40f573e58dd6feb7cbd74136059a531e0f`, including scoped v2 correctness, hermetic target-worker release build and clean tracked checkout.

Accepted contract:

- status derives directly from authoritative post/media/media-job state; no event table, progress table, SSE, WebSocket, Redis pub/sub, Kafka or second synchronization worker;
- user-visible phases are `waiting`, `processing`, `retrying`, `failed`, `ready`; no fabricated percentage;
- regeneration keeps last-known-good released media visible while replacement work is pending/running/retrying and after failure;
- raw worker `last_error` is not exposed;
- expired running leases derive as retrying/failed according to durable reclaim semantics without mutating the job;
- current unauthenticated v2 HTTP exposure returns status only for released, non-deleted posts with ready media; unreleased/deleted/non-visible posts use the same `404 post_not_found` shape;
- initial-ingestion status remains available behind the internal service boundary for later authenticated owner/visibility semantics;
- normal feed responses do not include status, so unused status adds no normal-feed SQL cost;
- frontend polling is one selected post every 2 seconds only while unresolved and stops on terminal state, 404, post change or unmount with `AbortController` cleanup;
- the reusable component is intentionally not mounted into the synthetic M1 benchmark app; M4 will mount it into the real connected board.

### Target status-query plan gate

Evidence package:

`m3-media-status-plan-20261005T005900Z.zip`

Uploaded SHA-256:

`8052f85a82d454c8431882b1756fb6491ee0183d62ec18a1586a4e4c79001ca7`

Manifest: **72/72 entries independently verified**. Exact tested executable SHA: `ba3265910ed7ede38e08e925a7cbdc0b63fdc9ef` in a detached candidate checkout.

PostgreSQL 17.11 used the production-shaped selected-post status query against the standard 100k seed plus a deliberate 10,001-kind-0-job history fixture for one post.

Accepted evidence:

- typical released post: one result; `posts_pkey`, `media_jobs_post_kind_id_idx`, `media_pkey`; **0.161 ms** plan execution;
- history-heavy post with 10,001 matching kind-0 jobs: `Limit` directly above `media_jobs_post_kind_id_idx`, one index row visited/returned; **0.171 ms** plan execution;
- no-job post: one released/ready result with null job fields; **0.143 ms** plan execution;
- missing post: no result and lateral/media subplans not executed; **0.080 ms** plan execution;
- all four plans had zero shared-buffer reads, zero temp spill, no sequential scan and no explicit sort;
- thirty prepared history-heavy executions included one initial **2.483 ms** call; the following 29 ranged **0.161-0.406 ms**, median **0.186 ms**;
- all correctness assertions passed and the status read is non-mutating.

Decision: **accept the query-plan gate and the progress/status slice**. Durable job history does not increase rows visited for the selected-post latest-job lookup. This evidence is a PostgreSQL plan gate, not a broader HTTP throughput benchmark. No API-coexistence rerun, cache, event stream or extra status infrastructure is justified.

Target cleanup removed the disposable PostgreSQL container and candidate checkout. Wallium PostgreSQL/Redis remained healthy and the canonical checkout remained clean. Raw target evidence may be deleted now that the uploaded ZIP has been independently validated.

## Retained architecture / invariants

Keep unless new evidence justifies change:

- nginx for TLS/static/media/reverse proxy;
- SolidJS + TypeScript + Vite + plain CSS;
- Go 1.25 + standard `net/http` + pgx/v5;
- PostgreSQL authoritative for app state and durable jobs;
- no Redis baseline dependency without measured need;
- Rust media worker and local NVMe media storage;
- immutable numeric relational IDs; never username as a foreign key;
- post-ID cursor pagination, never OFFSET;
- real search lexer/parser/AST and parameterized SQL only;
- filesystem/codec work outside DB transactions;
- stale/lost ownership can never release a post or authoritatively publish/complete work;
- deterministic/idempotent no-overwrite filesystem effects for at-least-once processing;
- one active media job per worker and one still-image/video-thumbnail AVIF encoder thread;
- production worker idle poll 500 ms; lease baseline 30 s; heartbeat around one third of lease;
- PostgreSQL worker connect/statement budget `min(lease / 10, 3s)`, 1 ms floor;
- output-affecting media changes require explicit processing-version identity;
- do not add Redis wakeups, cgroups, affinity, quotas, scheduler tuning or load admission without new evidence.

## Deferred work

Do not pull these into the next slice unless a concrete requirement makes them necessary:

- broader video transcoding;
- exact WebM/EBML acceptance;
- ingestion-source, processed-staging and superseded-output orphan janitor;
- deployment UID/GID/media-storage permissions;
- stronger `openat2`/`O_NOFOLLOW` hardening if the media-tree threat model changes;
- v1-v2 apples-to-apples end-to-end benchmark once equivalent behavior exists.

## Local-agent rule

Use local/server agents for browser/DevTools, SSH, real PostgreSQL, target benchmarks, temporary deployment or unavailable toolchains. Exact handed revisions must have applicable CI green first. Default permission is read-only/execution-only; no tracked/source/config/branch/deployment/persistent-state writes without explicit user approval. Return one evidence ZIP with exact SHA/status, commands, raw output, versions/environment, measurements/plans, errors and cleanup proof.

## Single best next task

Run the **target-host M4 authentication KDF + PostgreSQL auth query-plan gate** against exact executable candidate `33d72e7b5d56fe9bf8608b305984d4e150646fd5` (CI run `37256998256` green), then return the retained evidence ZIP for review here. Do not integrate the auth slice into `v2` until that evidence is reviewed and the password parameters/query shapes are accepted.
