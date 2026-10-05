# Ginbar v2 state / handoff

Last updated: 2026-10-05
Phase: **M4 connected core product in progress; connected board candidate is CI-green and awaiting browser/DevTools gate**
Integration branch: `v2`
Legacy branch: `master` (read-only for rewrite work)

Read this file first. Use [`PLAN.md`](PLAN.md) for stable milestone/product rules and [`PERFORMANCE.md`](PERFORMANCE.md) for accepted benchmark history. Do not use chat history as project memory.

## Current rewrite status

- M1 board performance prototype: **complete and integrated**.
- M2 fresh PostgreSQL schema + core Go API: **complete and integrated**.
- M3 media pipeline: **complete for the accepted v2 scope and integrated**.
- M4 connected core product: **in progress; authentication/session foundation is accepted and integrated, connected board/API/session candidate is CI-green with browser acceptance pending**.

## M4 connected board/API/session boundary — candidate, browser gate pending

Feature branch: `astra/m4-connected-board`, branched from `v2` at `6019dcabce1df1823bc5fa5352f4a26f2ddf945b`.

Exact executable candidate:

`e24105598e5c5440625126e29b29bd95201db5c1`

Applicable exact-candidate CI run `37261434103`: **success**, including all frontend tests, strict TypeScript checking, Vite production build, applicable worker-build skip verification and clean tracked checkout.

Implemented boundary:

- the real SolidJS board boots session state once from `GET /api/v2/auth/me`; `401` becomes signed-out state and other auth-read failures are isolated from the public board read path;
- root board boot uses the existing post-ID cursor feed only; direct `/post/:id` reconstruction uses the existing `/api/v2/posts/:id/around` endpoint only, avoiding an extra feed round trip;
- user selection of an already-retained post updates the selected row/expanded shell and canonical history path synchronously before any network work; direct links show a route shell immediately while around data is fetched;
- route state, ephemeral selection/keyboard state and retained server state are separate; no legacy v1 stores/components or new state-management dependency were introduced;
- same-row selection keeps the expanded slot in place; cross-row selection moves it below the newly selected row; Back/Forward reuses retained data when possible and reconstructs evicted direct-link context through the around endpoint;
- Arrow keys and J/K navigate the retained ID-descending window and request the next cursor/around window only when navigation reaches a retained boundary;
- feed pages are 120 posts, around radius is aligned to complete thumbnail rows up to the backend radius cap, and retained server state is bounded at 960 posts with row-sized trimming and selected-post preservation;
- newer-window prepends use viewport-anchor correction; row identity is keyed by the first retained post ID and selection uses Solid selectors so selection itself does not rebuild the server window;
- thumbnail and expanded media use authoritative v2 media dimensions; AVIF thumbnails are derived from the worker's canonical AVIF/MP4 storage-key contract with no extra API request;
- nginx now exposes only the processed `media/` subtree at `/media/` for immutable static delivery; ingestion `sources/` is not exposed by the added route;
- the accepted reusable `MediaStatus` component is mounted only for the selected expanded post, preserving its bounded polling and abort-on-change/unmount/terminal behavior;
- browser instrumentation is exposed only as `window.__ginbarM4` and records same-row/cross-row selection sync/frame timings, row mount/unmount counts, retained/DOM counts, long tasks and invariants for the pending browser gate;
- no backend feed/around/auth contract change, OFFSET pagination, Redis/JWT dependency, votes, tags, comments, uploads, profiles or moderation work was added in this slice.

Focused helper coverage validates row-aligned around radii, canonical/thumbnail media paths, ID-descending deduplicated merge behavior, whole-row bounded trimming, selected-post preservation and duplicate object identity. Exact-candidate CI passed **17/17 frontend tests**, `tsc --noEmit`, and the production Vite build. Candidate production output was `0.46 kB` HTML (`0.29 kB` gzip), `3.69 kB` CSS (`1.45 kB` gzip) and `32.67 kB` JavaScript (`12.14 kB` gzip), with source maps enabled.

No connected-board performance claim is accepted yet. Browser/DevTools evidence is still required for immediate shell timing, same-row/cross-row update scope, direct-link reconstruction, Back/Forward, Arrow/J/K navigation, incremental loading, the 960-post retention bound, viewport stability on newer prepends, selected-post polling cancellation/terminal behavior, real media/thumbnail delivery, production bundle behavior and actual row/DOM update scope. The target/browser gate should also syntax/serve-check the new `/media/` nginx mapping in an isolated configuration because the current scoped frontend CI does not lint nginx configuration.

Decision: **do not integrate or add votes/tags/comments yet**. The exact CI-green executable candidate must pass the browser/DevTools gate first.

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

## M4 authentication/session foundation — accepted and integrated

Original implementation branch: `astra/m4-auth-session`, branched from `v2` at `24aba0cda53641f7ef1cf107bbe7611b1803fca6`.

Reviewed candidate branch: `astra/m4-auth-session-review`, created as a descendant after the original feature branch was observed advancing concurrently during review so review corrections would not race or overwrite that branch.

Exact target-gated executable candidate:

`15e2d5d187d660fe81ecdc7864fe6f0e206a8a7b`

Accepted feature/evidence head fast-forwarded into `v2`:

`55d1f3b6c9569c94e7d4aecf907415e1483e6b93`

Post-fast-forward `v2` CI run `37259875155`: **success** on `55d1f3b6c9569c94e7d4aecf907415e1483e6b93`, including scoped v2 correctness, hermetic target-worker release build and clean tracked checkout.

Relevant commits:

- `fd72a4069859dda718d677c080dc67ff4677cf0c` — invitation registration, Argon2id password credentials, opaque PostgreSQL sessions, auth HTTP boundary, migration `006_auth_sessions.sql`, focused service/store/HTTP/security/concurrency tests and deterministic KDF benchmarks;
- `33d72e7b5d56fe9bf8608b305984d4e150646fd5` — align the Go workspace dependency graph with `x/text`'s required `x/sync v0.22.0` so readonly CI does not attempt to create `go.work.sum`;
- `b5582995d1171b0849c52ca4fb58186bf69c5650` and `e79c86281f9d618e8ba7803831f391b11de701fd` — state-only candidate/target-gate documentation;
- `5dd2733ffbfcb33e8926cdb3de4d58e8af1bf0a2` — reject oversized encoded password verifiers before split/base64 decode and add a deterministic realistic auth SQL query-plan fixture at `src/backend/v2/bench/auth_explain.sql`;
- `15e2d5d187d660fe81ecdc7864fe6f0e206a8a7b` — apply required Go formatting to the verifier bound; executable behavior is otherwise the same as `5dd2733...`;
- `e68cb64d21eb41ca8d4bc1e29374c4428cbe3ff9` — state-only commit pinning the exact target-gate candidate;
- `f95549d56cd04a1ed8b1701061293a086e347e7e` — record the accepted target KDF/query-plan evidence in `PERFORMANCE.md`; no executable change;
- `55d1f3b6c9569c94e7d4aecf907415e1483e6b93` — accept the auth target gate in project state; no executable change, then fast-forwarded into `v2`.

Applicable candidate CI:

- run `37256793597` on `fd72a4069859dda718d677c080dc67ff4677cf0c`: **failed before Go tests** because the readonly workspace needed the selected `golang.org/x/sync v0.22.0` checksum; Rust regression coverage had already passed and the tracked checkout remained clean;
- run `37256998256` on `33d72e7b5d56fe9bf8608b305984d4e150646fd5`: **success**, including scoped Go 1.25 formatting/vet/PostgreSQL-backed correctness, migration-triggered Rust regression checks, hermetic target-worker release build and clean tracked checkout;
- run `37257449979` on `5dd2733ffbfcb33e8926cdb3de4d58e8af1bf0a2`: **failed at the formatting gate only** because the new constant changed `gofmt` alignment; the tracked checkout remained clean and no target handoff was performed from this revision;
- run `37257570130` on exact executable candidate `15e2d5d187d660fe81ecdc7864fe6f0e206a8a7b`: **success**, including the applicable scoped v2 correctness gate, PostgreSQL-backed auth tests, migration-triggered checks, hermetic target-worker release build and clean tracked checkout.

Implemented contract:

- immutable numeric `users.id` remains relational identity; username is not an authorization/foreign-key identity;
- existing credential kind `0` is used as the initial password credential slot; existing user status `0` is treated as active/default according to the M2 schema semantics; `user_identities` remains independent for later OIDC/OAuth/passkey/email identities;
- no baseline ordinary-user role is created because current schema/product semantics do not require one;
- passwords use `golang.org/x/crypto/argon2` Argon2id with a self-describing verifier containing algorithm/version/parameters/salt/hash; password input is bounded to 12-1024 bytes before KDF work, stored verifier parameters are bounded before Argon2 allocation/work, and the complete encoded verifier is capped at 512 bytes before string splitting/base64 decoding so malformed persisted values cannot cause unbounded decode allocation;
- accepted Argon2id default is **64 MiB memory, one iteration, parallelism one, 16-byte random salt and 32-byte output**;
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

Focused tests cover password verifier round-trip/random salt/malformed verifier/input bounds including the encoded-verifier hard cap; invitation state and rollback; case-insensitive duplicate and same-invitation concurrency; session token hashing, independent sessions, expiry, revocation, user status and non-mutating lookup; cookie attributes; origin/CSRF policy; bounded HTTP bodies; stable unauthorized semantics; and secret non-echo behavior. Existing M1-M3 applicable regression checks remain green in CI.

### Target authentication acceptance gate — accepted

Evidence package:

`m4-auth-gate-20261005T031229Z.zip`

Uploaded/validated SHA-256:

`f792fa3d05da4db81f201bb983ec93f510c8a7c63074b6d32e0128f5479f3155`

Exact tested executable SHA: `15e2d5d187d660fe81ecdc7864fe6f0e206a8a7b`, with green pre-target CI run `37257570130`.

Target environment: Ubuntu 24.04.3, kernel 6.8.0-88-generic, i7-7700 (4 physical / 8 logical CPUs), 62 GiB RAM with about 46 GiB available at baseline. Native Go and `psql` were unavailable, so the committed benchmarks ran with Go 1.25.14 in a container and PostgreSQL 17.11 ran in an isolated disposable container with no published host port.

Accepted KDF evidence:

- default hash: **43.65-44.46 ms/op** across ten benchmark runs, about 67.1 MB/op;
- default verify: **43.60-44.31 ms/op**, about 67.1 MB/op;
- candidate `32MiB-t2`: **42.46-43.06 ms/op**;
- candidate `64MiB-t2`: **86.72-87.11 ms/op**;
- candidate `128MiB-t1`: **89.48-91.00 ms/op**;
- fixed 32-op parallel verify ranged **43.96-44.53 ms/op** at GOMAXPROCS=1, **23.86-26.35** at 2, **13.90-19.21** at 4 and **11.28-20.19** at 8;
- representative eight-way parallel run peaked at **1,120,756 KiB RSS** and about 553% CPU with no swapping.

The eight-way run is a real aggregate CPU/memory cost but not a target-host memory-capacity problem at the measured baseline. It does not justify weakening the KDF. Password KDF calls are not currently protected by a service-level concurrency limiter; abuse/rate limiting and any admission guard remain production-hardening concerns and should be introduced earlier only if connected-product evidence or exposure requirements justify them.

The committed SQL fixture used 100,000 users/credentials, 100,000 invitations and 500,000 sessions. PostgreSQL 17.11 used the intended indexes for every point path:

- session token hash -> active user: `user_sessions_token_hash_key` then `users_pkey`, **0.024 ms** execution;
- case-insensitive username -> password credential: `users_username_lower_uidx` then `user_credentials_user_id_kind_key`, **0.042 ms**;
- invitation availability: `invitations_token_hash_key`, **0.015 ms**;
- invitation registration `FOR UPDATE`: `LockRows` over `invitations_token_hash_key`, **0.019 ms**.

There were no sequential scans on the point-path tables, no unexpected row growth, explicit sort or temp spill.

Decision: **accept the authentication target gate and the 64 MiB/t=1/p=1 Argon2id default**. No KDF parameter change, auth lookup index, Redis/cache layer or broader API coexistence rerun is justified by this evidence. Durable measurements are recorded in `PERFORMANCE.md`.

Target cleanup removed the disposable PostgreSQL container and detached candidate worktree. The canonical checkout remained at its original branch/HEAD with no tracked changes; Wallium was untouched. The retained remote raw evidence directory may now be deleted because the uploaded archive has been independently validated.

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
- auth abuse/rate limits and KDF admission/concurrency controls until connected-product or production-hardening requirements justify their shape;
- v1-v2 apples-to-apples end-to-end benchmark once equivalent behavior exists.

## Local-agent rule

Use local/server agents for browser/DevTools, SSH, real PostgreSQL, target benchmarks, temporary deployment or unavailable toolchains. Exact handed revisions must have applicable CI green first. Default permission is read-only/execution-only; no tracked/source/config/branch/deployment/persistent-state writes without explicit user approval. Return one evidence ZIP with exact SHA/status, commands, raw output, versions/environment, measurements/plans, errors and cleanup proof.

## Single best next task

Run the browser/DevTools acceptance gate against exact CI-green executable candidate `e24105598e5c5440625126e29b29bd95201db5c1`: validate the connected real-API board and `/media/` delivery in an isolated disposable environment, measure immediate same-row/cross-row selection and row/DOM update scope, exercise direct-link reconstruction, Back/Forward, Arrow/J/K navigation, incremental cursor loading, the 960-post retention bound, newer-prepend viewport stability and selected-post media-status polling cleanup, and return retained raw evidence for primary-session review before integration or any votes/tags/comments work.
