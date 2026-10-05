# Ginbar v2 state / handoff

Last updated: 2026-10-05
Phase: **M4 connected core product in progress; search-connected board candidate is CI-green and browser-gate pending**
Integration branch: `v2`
Legacy branch: `master` (read-only for rewrite work)

Read this file first. Use [`PLAN.md`](PLAN.md) for stable milestone/product rules and [`PERFORMANCE.md`](PERFORMANCE.md) for accepted benchmark history. Do not use chat history as project memory.

## Current rewrite status

- M1 board performance prototype: **complete and integrated**.
- M2 fresh PostgreSQL schema + core Go API: **complete and integrated**.
- M3 media pipeline: **complete for the accepted v2 scope and integrated**.
- M4 connected core product: **in progress**.
  - authentication/session foundation: **accepted, target-gated and integrated**;
  - connected board/API/session boundary: **accepted, browser-gated and integrated**;
  - search-connected board: **implemented on feature branch, CI green, browser gate pending**;
  - votes, tag mutations, nested comments and profiles remain.

## M4 search-connected board — candidate, browser gate pending

Feature branch: `astra/m4-search-board`, branched from `v2` at:

`69964e5d6c6d5358b7ef4b69bb739f86b5ab0618`

Exact executable candidate:

`e6355939e8e547cd50905ecbe1b0e10943da8743`

Exact-candidate CI run `37266606785`: **success**. The correctness job completed the exact-revision checkout, scoped v2 correctness gate, applicable worker-build verification and clean tracked-checkout verification successfully.

Candidate implementation boundary:

- the frontend now carries the existing backend `q` contract through initial feed reads, cursor pagination and `/api/v2/posts/:id/around` reconstruction;
- effective search text is represented canonically as the `q` browser query parameter on both `/` and `/post/:id`; copied URLs, reload and browser history therefore retain search context;
- the frontend only trims the query string for canonical URL/state handling and does **not** duplicate the backend search lexer/parser/AST;
- include/exclude tags, quoted tags and score predicates remain backend-defined; malformed search returns the existing structured `invalid_search` error and the board shows the backend parser message;
- changing the effective query aborts stale initial/route/window work, bumps retained-window identity and clears the previous retained server window before rebuilding it under the new query;
- retained-post selection under an unchanged query remains synchronous and network-independent; selecting/closing a post preserves `q` in history;
- older pagination keeps post-ID cursor semantics and the active `q`; newer recovery and direct links use around with the same `q`;
- the accepted 960-post retention bound, ID-descending ordering, stable row identity and existing selection instrumentation remain unchanged;
- the existing backend search/feed/around SQL was not changed, so this candidate does not introduce a new hot SQL query shape requiring a new `EXPLAIN (ANALYZE, BUFFERS)` gate;
- no votes, tag mutations, comments, uploads, profiles, moderation, Redis, speculative cache or new state-management dependency was added.

Targeted automated coverage added for canonical query trimming/round-tripping and URL preservation, while existing backend tests continue to cover search grammar and malformed-query handling. Exact browser validation is still required before acceptance because this slice changes route/history/server-window behavior.

Unresolved acceptance gate:

- browser/DevTools evidence must verify empty/include/exclude/predicate/malformed searches, query changes, cursor pagination, searched `/post/:id` reconstruction, Back/Forward across query/post changes, Arrow/J/K inside searched windows, 960-post bounded retention, synchronous retained selection and stable row/update scope;
- retain the existing selection/Long Task/row-mount instrumentation and check for a regression rather than claiming a v1-v2 speedup.

Decision: **do not integrate this candidate into `v2` until the browser gate is accepted**.

## M4 connected board/API/session boundary — accepted and integrated

Feature branch: `astra/m4-connected-board`, branched from `v2` at `6019dcabce1df1823bc5fa5352f4a26f2ddf945b`.

Exact executable candidate:

`e24105598e5c5440625126e29b29bd95201db5c1`

Exact-candidate CI run `37261434103`: **success**, including 17/17 frontend tests, strict TypeScript checking, Vite production build, applicable worker-build verification and clean tracked checkout.

Accepted feature/evidence head fast-forwarded into `v2`:

`e2e202f2870c43903ad51cd0167d4712b318934b`

Post-fast-forward `v2` CI run `37265717474`: **success**, including the scoped v2 correctness gate, applicable worker-build verification and clean tracked checkout.

Browser evidence package:

`m4-connected-board-20261005T042618Z.zip`

Uploaded/validated SHA-256:

`124c847cb80a17ee6f4a2a01773ce32cb853b1aa77dee023074706d5541cd0a5`

Accepted implementation boundary:

- the SolidJS board boots session state once from `GET /api/v2/auth/me`; `401` becomes signed-out state without blocking public board reads;
- root board boot uses the existing post-ID cursor feed; direct `/post/:id` reconstruction uses the existing around endpoint without an unnecessary initial feed request;
- retained-post selection updates the expanded shell and canonical history path synchronously before network work;
- route state, ephemeral UI/selection state and retained server state remain separate; no legacy v1 store or new large state dependency was introduced;
- same-row selection replaces expanded content in place; cross-row selection moves it below the selected thumbnail row; Back/Forward and Arrow/J/K remain coherent;
- feed pages are 120 posts; around radius is row-aligned; retained server state is bounded at 960 posts with row-sized trimming and selected-post preservation;
- newer prepends use viewport-anchor correction; row identity stays stable and selection does not rebuild the board;
- media uses authoritative dimensions and deterministic worker storage keys; AVIF thumbnails are derived without an extra metadata API request;
- nginx exposes only the processed `media/` subtree at `/media/`; ingestion `sources/` is not mapped;
- `MediaStatus` is mounted only for the selected expanded post and retains bounded polling/abort behavior;
- no backend feed/around/auth contract change, OFFSET pagination, Redis/JWT dependency, votes, tags, comments, uploads, profiles or moderation work was added.

Accepted browser/DevTools evidence:

- desktop 1440x900: same-row and cross-row sync p95 **0.8 ms**; frame p95 **16.8 ms**; zero Long Tasks;
- warmed desktop and mobile selection produced **zero row mount/unmount deltas** and no feed/around request during pure retained selection;
- 1,200 distinct posts were fetched while retained state stayed **<=960**, IDs stayed unique/descending and both trim edges were exercised;
- retained 960-post state used 120 rows, 960 thumbnails, **4,119 DOM nodes** and about **12.3 MB** reported heap;
- newer-edge recovery preserved the 960-post bound; prepend-anchor residual was **-0.25 px**;
- a fresh delayed `/post/600` direct link showed its route shell at **42 ms**, made zero initial feed requests, then reconstructed 113 posts through one around request;
- real AVIF image, MP4 video, AVIF poster/thumbnail delivery and known dimensions decoded correctly;
- selected unresolved status polling stayed on the selected post at about 2 s intervals, stopped on switch/unmount, and stopped after terminal `ready`;
- signed-out and authenticated `/auth/me` behavior passed in the disposable browser runtime;
- isolated nginx `-t` and serve checks passed for `/media/`, while a decoy ingestion-source object remained inaccessible through that mapping.

The isolated non-default-port nginx auth POST probe returned `403 origin_not_allowed` because `proxy_set_header Host $host` omits the explicit port. Production default-port HTTPS is unaffected; retain this as a deployment caveat if non-default-port auth deployments are introduced later. One observed `net::ERR_ABORTED` media-status request was the expected `AbortController` cancellation during rapid reselection.

Decision: **accept the connected-board browser gate and bounded 960-post architecture**. No virtualization, larger store, Redis, event stream or other synchronization infrastructure is justified by this evidence. Durable browser measurements are recorded in `PERFORMANCE.md`.

## M4 authentication/session foundation — accepted and integrated

Exact target-gated executable candidate:

`15e2d5d187d660fe81ecdc7864fe6f0e206a8a7b`

Accepted feature/evidence head integrated before the connected-board slice:

`55d1f3b6c9569c94e7d4aecf907415e1483e6b93`

Post-fast-forward `v2` CI run `37259875155`: **success**.

Retained contract:

- immutable numeric `users.id` is relational identity; username is never a foreign key/authorization identity;
- invitations remain separate from identities/credentials so later OIDC/OAuth/passkey/email support does not redesign users;
- password credentials use bounded self-describing Argon2id verifiers; accepted default is **64 MiB, t=1, p=1, 16-byte salt, 32-byte output**;
- registration performs cheap validation and invitation precheck, hashes outside the transaction, then locks/revalidates and claims the invitation transactionally;
- session cookies are opaque random tokens; PostgreSQL stores only SHA-256 token hashes and numeric user IDs;
- session lifetime is fixed at 30 days; authenticated lookup is read-only and non-sliding; multiple sessions are allowed;
- browser auth endpoints are register/login/logout plus `GET /api/v2/auth/me`; auth POSTs require same-origin `Origin` metadata;
- production cookies are `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`;
- no JWT, Redis auth cache, speculative auth indexes or baseline ordinary-user role was added.

Auth target measurements and query plans are retained in `PERFORMANCE.md`. Abuse/rate limits and KDF admission/concurrency controls remain production-hardening concerns unless earlier connected-product evidence requires them.

## M3 accepted scope — complete and integrated

M3 includes:

- durable PostgreSQL media jobs with transactional claiming, retry/recovery, leases and generation fencing;
- upload/URL ingestion and durable source verification;
- deterministic generation-fenced DB publication and atomic no-overwrite local-NVMe publication;
- JPEG/PNG/WebP -> deterministic AVIF canonical + 256x256 AVIF thumbnail processing;
- compatible MP4 H.264/AAC zero-transcode canonical publication plus deterministic AVIF thumbnail;
- production Rust worker runner with renewal, cancellation, reconnect/backoff, graceful shutdown and crash/restart recovery;
- exact perceptual-dHash duplicate candidates with bounded PostgreSQL lookup;
- durable regeneration through the same processing/job/publication path while preserving last-known-good media;
- authoritative coarse media progress/status exposure with selected-post-only bounded frontend polling.

Broader video transcoding, exact WebM/EBML acceptance and operational orphan/janitor hardening remain deferred until a concrete requirement justifies them.

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
- route, ephemeral UI and server state remain separate;
- selected-post shell updates immediately; never wait for network before showing it;
- bounded 960-post board retention is accepted before virtualization;
- filesystem/codec work stays outside DB transactions;
- stale/lost worker ownership can never release a post or authoritatively publish/complete work;
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

Run the browser/DevTools acceptance gate against exact executable candidate `e6355939e8e547cd50905ecbe1b0e10943da8743` on `astra/m4-search-board`, using read-only/execution-only local-agent permissions. Validate searched root/deep-link/history/pagination/keyboard/retention behavior and selection/update-scope instrumentation, return one retained evidence ZIP, then inspect and accept or fix the candidate before any fast-forward into `v2`.
