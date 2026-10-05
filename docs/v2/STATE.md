# Ginbar v2 state / handoff

Last updated: 2026-10-05
Phase: **M4 connected core product in progress; tag-mutation SQL/API evidence accepted, focused browser supplement pending**
Integration branch: `v2`
Legacy branch: `master` (read-only for rewrite work)

Read this file first. Use [`PLAN.md`](PLAN.md) for stable milestone/product rules and [`PERFORMANCE.md`](PERFORMANCE.md) for accepted benchmark history. Do not use chat history as project memory.

## Current rewrite status

- M1 board performance prototype: **complete and integrated**.
- M2 fresh PostgreSQL schema + core Go API: **complete and integrated**.
- M3 media pipeline accepted scope: **complete and integrated**.
- M4 connected core product: **in progress**.
  - authentication/session foundation: **accepted and integrated**;
  - connected board/API/session boundary: **accepted and integrated**;
  - search-connected board: **accepted and integrated**;
  - post voting: **accepted, SQL/browser-gated and integrated**;
  - nested comments read/create: **accepted, SQL/browser-gated and integrated**;
  - comment voting: **accepted, SQL/browser-gated and integrated**;
  - tag mutations: **implementation candidate complete; exact-candidate CI green; SQL/API evidence accepted; focused browser supplement pending**;
  - profiles remain afterward.

## M4 tag mutations — implementation candidate; browser supplement pending

Verified live GitHub `v2` base before this slice:

`e7da7d7ed80599cffd0caf9c32e0db586366265a`

Implementation branch:

`astra/m4-tag-mutations`

Exact executable candidate:

`1d67cfde2847a3b10aa44bb799ae878a39e36a55`

Exact-candidate `v2 CI` run `37359169575`, job `111929122699`: **success**.

- exact SHA checkout/verification passed;
- scoped v2 correctness gate passed, including PostgreSQL-backed tag mutation tests and retained v2 suites;
- frontend helper tests, TypeScript/build checks and retained board tests passed through the normal v2 gate;
- target-worker release-build verification passed;
- tracked checkout remained clean.

### Candidate implementation boundary

- Public selected-post tag state is `GET /api/v2/posts/:id/tags`; authenticated users add with `POST /api/v2/posts/:id/tags`; authenticated moderator/admin users remove by immutable numeric tag ID with `DELETE /api/v2/posts/:id/tags/:tagId`.
- Add/remove handlers retain the accepted same-origin mutation boundary and use immutable authenticated `user_id`; removal authorization is resolved from authoritative `user_roles` rows inside the PostgreSQL transaction rather than client/session role claims.
- Tag names are valid UTF-8, trimmed, 1..80 runes, lower-case normalized for identity/search matching, and reject control characters plus quote/backslash forms the current search grammar cannot represent safely. Existing normalized tag entities are reused.
- Repeated active addition is idempotent. The existing `(post_id, tag_id)` primary key prevents duplicate relations; an existing soft-removed relation is reactivated atomically. Concurrent creation of the same normalized tag uses the existing normalized-name uniqueness constraint rather than process-local synchronization.
- Addition first locks a released/nondeleted target post and unavailable/deleted posts do not create a tag/relation. Removal requires an authoritative moderator/admin role, validates the same eligible post boundary, and treats an absent/inactive relation as an idempotent no-op.
- PostgreSQL remains authoritative. No schema migration, Redis dependency, event stream, cache, process-local mutation lock or speculative index was added.
- Existing feed/around row shape and search SQL are unchanged. Search continues to match active `post_tags` using existing include/exclude semantics. The selected-post tag endpoint avoids adding per-post tag materialization work to every hot feed/around row.
- Add/remove responses return the full authoritative selected-post tag snapshot, including whether the current viewer may remove tags. Successful frontend mutations therefore do not reload feed/around solely for reconciliation.
- Frontend tag state is local to the expanded post and authoritative-first. Selection changes abort/invalidate old load/mutation work by post ID plus epoch; old-selection or mismatched-post responses cannot update the newly selected post.
- Deterministic 400/401/403/404 mutation failures retain the last authoritative snapshot without an extra read. Transport failures and 5xx responses can have ambiguous commit outcomes, so the frontend performs one focused post-tag GET and applies it only if the same post/epoch is still active; failed reconciliation keeps the last authoritative snapshot plus the visible mutation error.
- The board shell still updates immediately on selection; tag loading is independent of shell rendering. No large global store or whole-board replacement was introduced.

### Candidate correctness coverage

The exact-candidate suites cover normalization/validation, authenticated add, idempotence/reactivation, unavailable-post rejection, moderator/admin removal, ordinary-user rejection, deterministic absent/inactive removal, concurrent same-tag creation, concurrent add/remove consistency, immediate search semantics, signed-out/ordinary/moderator capability state, same-origin rejection, authoritative response state, stale-selection/epoch isolation, ambiguous-failure reconciliation classification, and retained auth/feed/search/post-vote/comment/comment-vote suites.

### Retained gate evidence inspected here

Evidence package:

`m4-tag-20261005T185611Z.zip`

Independently verified SHA-256:

`a9ed36119c982a73bd2de0bfd756035dde027bc7d298ebbe7c281d0f42f13e5d`

Archive integrity passed with 74 entries. Raw `findings.md`, SQL plans, API/database-state results, synchronized conflict evidence, browser results/trace, exact CI metadata and cleanup evidence were inspected here. Exact tested executable was `1d67cfde2847a3b10aa44bb799ae878a39e36a55`.

#### Accepted PostgreSQL evidence

The disposable PostgreSQL 16.15 fixture applied all six committed migrations and contained 100,000 posts, 100 baseline tags, 300,000 active post/tag relations and 1,000 baseline users before gate-specific rows.

`EXPLAIN (ANALYZE, BUFFERS)` covered signed-out/authenticated selected-post snapshots, released/nondeleted post validation/lock, normalized tag lookup/create/conflict reload, relation insert/reactivation, moderator/admin authorization, active relation removal and post-mutation snapshot. Candidate tag-plan execution times were 0.008–0.204 ms on the disposable host. Large relations used bounded/index-backed shapes; PostgreSQL only chose sequential scans for the tiny `tags` and `user_roles` dimensions. Snapshot/role sorts were 25 KiB in-memory quicksorts.

Existing feed/search shapes remained bounded at the realistic fixture size: first page 0.098 ms, old cursor 0.117 ms, include-tag+score 0.874 ms, include+exclude-tag+score 1.056 ms, and around-post 0.131 ms. No candidate-attributable large-table sequential scan, unbounded large-table sort, temp spill or application-level N+1 query pattern was observed. These timings establish boundedness/regression acceptance only, not a speedup claim. Existing indexes are sufficient; no new index is justified by this evidence.

#### Accepted API/database-state evidence

The retained evidence established normalized and repeated/idempotent adds, soft-removed relation reactivation, moderator/admin removal metadata, ordinary-user 403 with unchanged relation state, absent-relation no-op behavior, repeated inactive removal preserving metadata, concurrent add/remove consistency, missing/deleted/unavailable post rejection without orphan rows, and immediate include/exclude search consistency.

A synchronized same-name create test forced two candidate `INSERT INTO tags` statements to wait concurrently under a held table lock; after release both requests returned 200, producing exactly one normalized tag and two post relations. Final direct checks found zero duplicate normalized names and zero broken removal-metadata rows.

Signed-out add/remove returned 401 and cross-origin add/remove returned 403 with zero unauthorized database mutation. Disposable state and processes were cleaned up; canonical tracked status remained clean.

#### Browser evidence already accepted

The real-browser evidence established immediate expanded-shell rendering while tag state loaded independently; ordinary-user add/repeat behavior; no ordinary remove controls; moderator/admin remove controls and authoritative response reconciliation; signed-out and corrected cross-origin rejection; no feed/around reload on successful mutation; retained unrelated thumbnail DOM identity; and a delayed same-row old-post tag read that could not leak stale tag state after selection changed.

The ordinary-user deterministic 403 scenario also retained the initial tag GET count across the failed removal, which is consistent with the candidate rule that deterministic client/auth failures do not trigger ambiguous-outcome reconciliation.

### Missing acceptance evidence

Do **not** integrate this slice into `v2` yet. The first gate did not exercise several browser checks explicitly required for acceptance, most importantly the executable behavior added after the original candidate:

- no ambiguous transport/5xx mutation-outcome test was run, so there is no real-browser proof that a mutation which committed server-side but surfaced as a browser-visible 5xx performs exactly one focused `GET /api/v2/posts/:id/tags`, restores authoritative state, keeps the visible mutation error, and avoids feed/around reconciliation;
- no delayed **mutation response** selection test was run; the retained stale-selection evidence only delayed a tag read;
- no cross-row stale read/mutation case was retained;
- route/search/Back/Forward/Arrow/J/K coherence, existing comment UI coherence, retained descending/unique/bounded IDs, `window.__ginbarM4.assertInvariants()`, and representative Long Tasks were not retained in this tag gate.

These are evidence gaps, not observed application failures. The PostgreSQL/API portion and the browser scenarios listed above are accepted and should not be rerun unless the executable changes.

Decision: **do not accept or integrate M4 tag mutations yet**. Run a focused browser supplement against the same exact executable SHA; no SQL rerun is required if the executable remains unchanged.

## M4 comment voting — accepted and integrated

Exact executable candidate: `005b10ffa4b6f344d64ae6b0e9b5246a15527244`.

Exact-candidate `v2 CI` run `37342739594`, job `111873754474`: **success**.

Accepted evidence package: `m4-comment-voting-20261005T170504Z.zip`, SHA-256 `481b990f4c3d4ac6a8f1e7b4b504733b7b80fef8d5e199e4d1ab74d2e234e356`.

Accepted boundary: PostgreSQL-authoritative explicit `-1/0/+1` comment voting; bounded viewer-vote reads; row-locked score deltas; per-comment frontend sequencing; optimistic rollback; stale-selection isolation; no feed/around/comment-page reconciliation on success. Realistic SQL plans were bounded/index-backed and browser evidence covered transition correctness, failure rollback, rapid competing actions, independent-comment concurrency, navigation/update-scope invariants and Long Tasks.

Accepted history was fast-forwarded to `v2`; post-integration `v2 CI` run `37351495807`, job `111903167789`, succeeded. No blocker remains from this slice.

## M4 nested comments read/create — accepted and integrated

Exact executable candidate: `d83ffa69c6a80418f13c5e4a2b5af19004846a51`.

Exact-candidate `v2 CI` run `37334651076`, job `111846241798`: **success**.

Accepted evidence package: `m4-comments-20261005T000000Z.zip`, SHA-256 `8c1ca3f71753e449db451c6ed33412bf9451bc4e2345f92b62a64b18e764e7cf`.

Accepted boundary: bounded public ascending comment-ID cursor reads; deleted structural tombstones; authenticated same-origin top-level/reply creation; exact body validation; one-statement validated PostgreSQL creation; local selected-post state; abort/epoch guards; iterative tree construction; authoritative-ID merge across delayed reads/creates. SQL/browser evidence established bounded plans, nested/deleted tree correctness, create identity, race/failure isolation and navigation/update-scope invariants.

Post-integration `v2 CI` run `37339584913`, job `111862933910`, succeeded. No blocker remains from this slice.

## M4 post voting — accepted and integrated

Exact executable candidate: `e1c5d1f65e72a81615164bc6445bcdc2d8218381`.

Exact-candidate CI run `37313953385`: **success**.

Accepted evidence package: `m4-post-voting-20261005T133638Z.zip`, SHA-256 `4cf5c5839e7cfc427465821600bcf08a3bb56e9b5a22dbdead4fd185ca396b14`.

Post voting is PostgreSQL-authoritative, uses explicit `-1/0/+1` state, serializes score mutations with the post row lock, exposes viewer vote in bounded feed/around reads, and updates retained frontend post state optimistically without feed/around reloads or board-row remounts. The accepted browser gate covered transitions, rollback, rapid competing actions, history/search/navigation invariants, signed-out behavior and zero warmed feed/around refreshes.

Post-integration `v2 CI` run `37321333276` succeeded. No blocker remains from this slice.

## Retained M4 board/auth/search decisions

- session identity is immutable numeric `users.id`; usernames are never relational authorization identity;
- invitation, identity and credentials remain separate for later OAuth/OIDC/passkey/email expansion;
- PostgreSQL-backed opaque sessions remain the baseline; no JWT/Redis auth cache;
- board root uses post-ID cursor feed; direct post reconstruction uses bounded around queries without an unnecessary initial feed;
- search `q` is canonical across root/post routes and carried through feed pagination and around reconstruction;
- selected-post shell updates immediately before network work;
- route, ephemeral UI and retained server state remain separate;
- feed pages remain bounded and retained board state is capped at 960 posts before considering virtualization;
- stable row identity, viewport-anchor correction and targeted retained-post mutation remain required;
- selected-post media-status polling remains bounded and abortable;
- nginx exposes processed media only, not ingestion sources;
- the isolated non-default-port nginx auth caveat remains: `$host` omits an explicit non-default port, while production default-port HTTPS is unaffected.

## Retained architecture / invariants

- nginx for TLS/static frontend/media/reverse proxy;
- SolidJS + TypeScript + Vite + plain CSS;
- Go + standard `net/http` + pgx/v5;
- PostgreSQL authoritative for application state and durable jobs;
- no Redis baseline dependency without measured need;
- Rust media worker + local NVMe media storage;
- immutable numeric relational IDs; never username as a foreign key;
- post-ID cursor pagination, never OFFSET;
- real search lexer/parser/AST and parameterized SQL only;
- selected-post UI updates immediately and never waits for network;
- bounded 960-post board retention before virtualization;
- stable row identity and targeted retained-post updates;
- hot SQL bounded/indexed for actual query shapes and checked with `EXPLAIN (ANALYZE, BUFFERS)` when changed;
- filesystem/codec work stays outside DB transactions;
- media worker concurrency remains explicitly bounded.

## Deferred work

Do not pull these into the next slice without a concrete requirement:

- broader video transcoding or exact WebM/EBML acceptance;
- media orphan/janitor hardening;
- deployment UID/GID/media-storage permissions;
- stronger filesystem hardening if the media-tree threat model changes;
- auth abuse/rate limits and KDF admission controls before production-hardening evidence requires them;
- virtualization, Redis synchronization, event streams or a large frontend store;
- v1-v2 end-to-end speedup claims before an apples-to-apples benchmark exists.

## Unresolved issues

- Tag mutations are not yet accepted solely because the focused browser supplement above is missing against exact executable `1d67cfde2847a3b10aa44bb799ae878a39e36a55`.
- No SQL/index/schema change is justified by the retained tag evidence. The realistic fixture's bounded selected-post cardinality did not show a need for a numeric per-post tag cap or pagination; revisit only if product requirements or later evidence justify it.

No unresolved correctness, SQL-plan, browser-performance, integration or architecture blocker remains from the accepted post-voting/comment slices.

## Single best next task

Run a focused **M4 tag-mutation browser supplement** against exact executable `1d67cfde2847a3b10aa44bb799ae878a39e36a55` after confirming exact-candidate CI run `37359169575` / job `111929122699` remains green. Reuse the real candidate API/PostgreSQL/browser stack but do not rerun the accepted SQL plan suite. Retain evidence for ambiguous committed-but-browser-5xx reconciliation, deterministic-failure no-reconciliation behavior, delayed mutation-response selection isolation, cross-row stale-state isolation, route/search/Back/Forward/Arrow/J/K/comment coherence, retained board invariants and representative Long Tasks. Return one standard evidence ZIP for inspection here before any integration into `v2`.
