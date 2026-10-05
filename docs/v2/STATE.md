# Ginbar v2 state / handoff

Last updated: 2026-10-05
Phase: **M4 connected core product in progress; search-connected board browser gate accepted, integration pending**
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
  - search-connected board: **accepted by exact-candidate CI + browser/DevTools gate; integration into `v2` is the immediate remaining action**;
  - post/comment votes, tag mutations, nested comments and profiles remain.

## M4 search-connected board — accepted browser gate

Feature branch: `astra/m4-search-board`, branched from `v2` at:

`69964e5d6c6d5358b7ef4b69bb739f86b5ab0618`

Exact executable candidate:

`e6355939e8e547cd50905ecbe1b0e10943da8743`

Exact-candidate CI run `37266606785`: **success**. It completed the exact-revision checkout, scoped v2 correctness gate, applicable worker-build verification and clean tracked-checkout verification.

Browser evidence package:

`m4-search-board-20261005T052100Z.zip`

Uploaded/independently validated SHA-256:

`e18d10e57c963dffecc22c309c2710659a8c0eb3f0c095f6387728d1acf6009b`

Archive integrity check passed with no corrupt ZIP entries. Raw JSON contains **45/45 passing check records** across the main, retention, direct-link, copied-root and media-status runs. `findings.md` says 41/41; that count is stale, but there are no failed raw checks.

Accepted implementation boundary:

- the frontend carries the existing backend `q` contract through initial feed reads, post-ID cursor pagination and `/api/v2/posts/:id/around` reconstruction;
- effective search text is represented canonically as `q` on both `/` and `/post/:id`, so copied URLs, reload and browser history retain search context;
- the frontend only trims the query for canonical state and does **not** duplicate the backend search lexer/parser/AST;
- include/exclude tags and score predicates remain backend-defined; malformed search surfaces the structured backend `invalid_search` parser message instead of silently falling back to an unfiltered feed;
- changing the effective query aborts stale route/window work and replaces the retained server window before rebuilding under the new query;
- retained-post selection under an unchanged query stays synchronous and does not issue feed/around requests;
- older pagination keeps post-ID cursor semantics plus the active `q`; newer-edge recovery and direct links use around with the same `q`;
- the accepted 960-post bound, ID-descending ordering, stable row identity and targeted selected-post reactivity are preserved;
- no backend search/feed/around SQL changed, so this slice introduces no new hot SQL shape requiring another `EXPLAIN (ANALYZE, BUFFERS)` gate;
- no votes, tag mutations, comments, uploads, profiles, moderation, Redis or new state-management dependency was added.

Accepted browser/DevTools evidence:

- normal empty-query board, include `sunset`, exclude `-anime`, combined `sunset -anime`, and `score:>=100` all returned the expected fixture subsets;
- malformed `score:100` retained the query in the URL, showed the backend parser error and retained zero posts;
- searched feed pagination preserved `q`; the separate retention run explicitly exercised newer-edge around recovery with `q=common`;
- query changes rebuilt from a fresh cursor with no stale nonmatching retained posts;
- Back/Forward restored search windows and searched post selections coherently;
- fresh `/post/1000?q=sunset` boot issued no initial feed request, showed the route shell before the deliberately delayed around response completed, and reconstructed through around with `q=sunset`;
- reload of the searched post used around only, and a copied searched root reconstructed through feed with the same query;
- Arrow/J/K navigation stayed inside the searched retained window;
- the `common` 1,100-result drain reached a maximum retained count of **960**, ended at 956 after full drain, preserved strict descending unique IDs, exercised both-edge trimming and successfully recovered the newer edge;
- warmed searched selection produced **0 row mounts / 0 row unmounts** and **0 Long Tasks**;
- desktop 1440x900, 120 iterations each: same-row sync p95 **0.5 ms**, cross-row sync p95 **0.5 ms**, both frame p95 **16.8 ms**; cross-row sync max **0.8 ms** and frame max **17.2 ms**;
- end-of-main-run reported heap was about **6.7 MB**;
- ready selected media status fetched once and did not repeat; unresolved regeneration polled at ~2.0 s gaps; terminal state stopped repeating; Escape stopped polling in all tested modes.

Expected/nonblocking evidence noise:

- signed-out `/api/v2/auth/me` produced the expected 401 console resource entry;
- the intentional malformed search produced the expected 400 console resource entry;
- one media-status request was `net::ERR_ABORTED` during rapid benchmark reselection, matching designed `AbortController` cancellation;
- the main-script `newer-edge-q` check had no around request in that particular window and was therefore vacuous, but the dedicated retention gate explicitly forced newer-edge recovery and verified `/around?...&q=common`.

The disposable browser runtime, API, nginx, database, fixtures and worktree were cleaned up successfully. The agent reported no tracked-source/config/branch writes and no production/persistent application-state changes. Its canonical local checkout had a pre-existing local `v2` HEAD different from GitHub; remote GitHub `v2` remained authoritative and was independently verified before integration work.

Decision: **accept the M4 search-connected board slice**. The evidence shows no meaningful selection/update-scope regression relative to the accepted connected-board harness, and no additional frontend architecture or backend search changes are justified.

## Retained M4 connected-board/auth decisions

Keep the already accepted boundaries unless new evidence requires change:

- session identity is immutable numeric `users.id`; usernames are never relational authorization identity;
- invitation, identity and credentials remain separate for later OAuth/OIDC/passkey/email expansion;
- opaque PostgreSQL-backed session tokens remain the baseline; no JWT/Redis auth cache;
- board root uses post-ID cursor feed; direct post reconstruction uses around without an unnecessary initial feed;
- selected-post shell updates immediately before network work;
- route, ephemeral UI and retained server state remain separate;
- feed pages are 120 posts and retained server state is bounded at 960 posts before considering virtualization;
- stable row identity and viewport-anchor correction remain required;
- selected-post media-status polling remains bounded and abortable;
- nginx exposes processed `media/` only, not ingestion `sources/`;
- the isolated non-default-port nginx auth caveat remains: `$host` omits an explicit non-default port, while production default-port HTTPS is unaffected.

## Retained architecture / invariants

- nginx for TLS/static/media/reverse proxy;
- SolidJS + TypeScript + Vite + plain CSS;
- Go 1.25 + standard `net/http` + pgx/v5;
- PostgreSQL authoritative for application state and durable jobs;
- no Redis baseline dependency without measured need;
- Rust media worker + local NVMe media storage;
- immutable numeric relational IDs; never username as a foreign key;
- post-ID cursor pagination, never OFFSET;
- real search lexer/parser/AST and parameterized SQL only;
- selected-post UI updates immediately and never waits for network;
- bounded 960-post board retention is accepted before virtualization;
- hot SQL must be bounded/indexed for actual query shapes and checked with `EXPLAIN (ANALYZE, BUFFERS)` when changed;
- filesystem/codec work stays outside DB transactions;
- one active media job per worker remains the accepted baseline until measurements justify otherwise.

## Deferred work

Do not pull these into the next M4 slice without a concrete requirement:

- broader video transcoding or exact WebM/EBML acceptance;
- media orphan/janitor hardening;
- deployment UID/GID/media-storage permissions;
- stronger filesystem hardening if the media-tree threat model changes;
- auth abuse/rate limits and KDF admission controls before production-hardening evidence requires them;
- virtualization, Redis synchronization, event streams or a large frontend store;
- v1-v2 end-to-end speedup claims before an apples-to-apples benchmark exists.

## Local-agent rule

Use local/server agents for browser/DevTools, SSH, real PostgreSQL, target benchmarks, temporary deployment or unavailable toolchains. Exact handed executable revisions must have applicable CI green first. Default permission is read-only/execution-only; no tracked/source/config/branch/deployment/persistent-state writes without explicit user approval. Return one evidence ZIP with exact SHA/status, commands, raw output, versions/environment, measurements/plans, errors and cleanup proof.

## Single best next task

Fast-forward the accepted `astra/m4-search-board` evidence head into current GitHub `v2`, run/verify the resulting `v2` CI, then record the integrated head and leave the next product slice explicit before further feature work.
