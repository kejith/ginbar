# Ginbar v2 state / handoff

Last updated: 2026-10-06
Phase: **M4 connected core product in progress; tag mutations accepted, integration pending**
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
  - tag mutations: **accepted after SQL/API and supplemental browser gates; integration pending**;
  - profiles remain afterward.

## M4 tag mutations — accepted; integration pending

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

### Accepted implementation boundary

- Public selected-post tag state is `GET /api/v2/posts/:id/tags`; authenticated users add with `POST /api/v2/posts/:id/tags`; authenticated moderator/admin users remove by immutable numeric tag ID with `DELETE /api/v2/posts/:id/tags/:tagId`.
- Add/remove retain the same-origin mutation boundary and immutable authenticated `user_id`; removal authorization is resolved from authoritative `user_roles` rows inside the PostgreSQL transaction.
- Tag names are valid UTF-8, trimmed, 1..80 runes, normalized to lower case for identity/search matching, and reject controls plus quote/backslash forms the current search grammar cannot represent safely.
- Repeated active addition is idempotent. `(post_id, tag_id)` prevents duplicate relations; soft-removed relations reactivate atomically. Concurrent creation of one normalized tag relies on the normalized-name uniqueness constraint rather than process-local synchronization.
- Addition locks a released/nondeleted target post before tag creation. Unavailable/deleted posts do not create orphan tags/relations. Removal requires moderator/admin, validates the same eligible-post boundary, and treats absent/inactive relations as idempotent no-ops.
- PostgreSQL remains authoritative. No migration, Redis dependency, event stream, cache, process-local mutation lock or speculative index was added.
- Existing feed/around row shape and search SQL are unchanged. Search continues to use active `post_tags`; tag state is loaded only for the selected post rather than materialized onto every hot feed row.
- Add/remove responses return the full authoritative selected-post snapshot. Successful mutations therefore do not reload feed/around solely for reconciliation.
- Frontend tag state is selected-post-local. Post ID + epoch + abort guards fence delayed loads and mutation responses from newer selections.
- Deterministic 400/401/403/404 failures retain the last authoritative snapshot without an extra read. Transport/5xx failures have ambiguous commit outcome, so the frontend performs one focused tag GET; the result applies only while the same post/epoch remains active. A failed reconciliation retains the last authoritative snapshot plus the visible mutation error.
- Expanded shell rendering remains immediate and independent of tag loading. No large global store or whole-board replacement was introduced.

### Accepted correctness coverage

Exact-candidate suites cover normalization/validation, authenticated add, idempotence/reactivation, unavailable-post rejection, moderator/admin removal, ordinary-user rejection, absent/inactive removal, concurrent same-tag creation, concurrent add/remove consistency, immediate search semantics, capability state, same-origin rejection, authoritative response state, stale-selection/epoch isolation, ambiguous-failure classification, and retained auth/feed/search/post-vote/comment/comment-vote suites.

### Accepted PostgreSQL/API gate

Evidence package:

`m4-tag-20261005T185611Z.zip`

Independently verified SHA-256:

`a9ed36119c982a73bd2de0bfd756035dde027bc7d298ebbe7c281d0f42f13e5d`

Archive integrity passed and raw SQL plans, DB/API state, synchronized conflict evidence, browser evidence, CI metadata and cleanup evidence were inspected. Exact tested executable was `1d67cfde2847a3b10aa44bb799ae878a39e36a55`.

The disposable PostgreSQL 16.15 fixture applied all six committed migrations and contained 100,000 posts, 100 baseline tags, 300,000 active post/tag relations and 1,000 baseline users before gate-specific rows.

`EXPLAIN (ANALYZE, BUFFERS)` covered signed-out/authenticated selected-post snapshots, released/nondeleted post validation/lock, normalized tag lookup/create/conflict reload, relation insert/reactivation, moderator/admin authorization, active relation removal and post-mutation snapshot. Candidate tag-plan execution times were 0.008–0.204 ms on that disposable host. Large relations used bounded/index-backed shapes; only the tiny `tags` and `user_roles` dimensions used sequential scans. Snapshot/role sorts were 25 KiB in-memory quicksorts.

Existing feed/search shapes remained bounded: first page 0.098 ms, old cursor 0.117 ms, include-tag+score 0.874 ms, include+exclude-tag+score 1.056 ms, around-post 0.131 ms. There was no candidate-attributable large-table sequential scan, unbounded large-table sort, temp spill or application-level N+1 pattern. These are boundedness observations, not speedup claims. Existing indexes are sufficient; no new index or numeric per-post tag cap is justified by this evidence.

The controlled API/database-state gate established normalized and repeated adds, reactivation, moderator/admin removal metadata, ordinary-user rejection with zero mutation, absent/inactive removal semantics, concurrent add/remove consistency, unavailable-post rejection without orphan rows, and immediate include/exclude search consistency. A synchronized same-name create test forced two tag inserts to wait concurrently and then complete with one normalized tag plus two relations. Final checks found zero duplicate normalized names and zero broken removal metadata rows. Signed-out mutations returned 401 and cross-origin mutations returned 403 with zero unauthorized DB mutation.

### Accepted supplemental browser gate

Evidence package:

`m4-tag-browser-supplement-20261006T072000Z.zip`

Independently verified SHA-256:

`238a863eedceb9bd0fdff161d985fe2b6ef6073c3a39e3c33b139999169a27ae`

Archive integrity passed with 229 entries. Raw `findings.md`, `scenario-assertions.json`, browser request/response ordering, DB proofs, Playwright trace, exact CI metadata and cleanup evidence were inspected. Exact executable was again `1d67cfde2847a3b10aa44bb799ae878a39e36a55`. The scenario file contains **34/34 passing assertions** and no failed assertions.

The supplemental real-browser gate established:

- a real tag add committed in PostgreSQL before the browser received a synthetic 503; direct DB state proved the active relation;
- the visible failure triggered exactly one focused `GET /api/v2/posts/:id/tags`, restored the committed authoritative state, kept usable failure state/controls, and generated zero feed/around requests;
- a delayed reconciliation response delivered after a cross-row selection change could not contaminate the new post or carry the old mutation error forward;
- a successful mutation response delayed until after a cross-row selection change could not update the new selection;
- a delayed old-post tag GET delivered after a cross-row change could not leak old tag state, and expanded placement remained under the newly selected thumbnail row;
- canonical post routes and search `q` remained coherent through Back/Forward, ArrowRight and J/K navigation;
- existing comment UI remained coherent and usable;
- an unrelated thumbnail retained DOM identity through representative tag interaction;
- retained IDs were descending and unique, retained count stayed below the 960-post bound, and `window.__ginbarM4.assertInvariants()` returned `{ "ok": true, "errors": [] }`;
- a warmed add/remove/cross-post-selection interaction recorded **0 Long Tasks** with an explicit `PerformanceObserver(type=longtask)`.

Expected synthetic 5xx and initial signed-out console responses were separated from failures; the final browser run had no failed requests or page errors. Browser-only request interception was used only to delay/deliver real responses for stale-response fencing; application source was unchanged.

Both gate runs cleaned their disposable databases/services/worktrees and preserved canonical tracked status. No prohibited tracked-source, SQL, config, ref, deployment or production-state write occurred during either gate.

Decision: **accept M4 tag mutations**. The candidate is PostgreSQL-authoritative, concurrency-safe for the tested mutation shapes, bounded/index-backed at realistic scale, correctly reconciles ambiguous commit outcomes, fences stale selected-post work, preserves board/navigation/update-scope invariants, and introduces no measured representative Long Task. No additional cache, global tag store, index, event stream, polling layer, pagination/cap, or virtualization change is justified by the evidence.

### Integration status

Acceptance is complete. Integration is still pending at this documentation commit. Before updating `v2`, re-verify that remote `v2` is still `e7da7d7ed80599cffd0caf9c32e0db586366265a` and that the accepted branch is a pure fast-forward descendant. Use a non-force ref update only; then require post-fast-forward `v2 CI` success before closing the slice.

## Other accepted M4 slices

### Comment voting

Exact executable `005b10ffa4b6f344d64ae6b0e9b5246a15527244`; exact-candidate CI run `37342739594`, job `111873754474`, success. Accepted evidence package `m4-comment-voting-20261005T170504Z.zip`, SHA-256 `481b990f4c3d4ac6a8f1e7b4b504733b7b80fef8d5e199e4d1ab74d2e234e356`. PostgreSQL-authoritative explicit `-1/0/+1` voting, bounded viewer-vote reads, row-locked score deltas, per-comment frontend sequencing, optimistic rollback and stale-selection isolation were accepted. Post-integration `v2 CI` run `37351495807`, job `111903167789`, succeeded.

### Nested comments read/create

Exact executable `d83ffa69c6a80418f13c5e4a2b5af19004846a51`; exact-candidate CI run `37334651076`, job `111846241798`, success. Accepted package `m4-comments-20261005T000000Z.zip`, SHA-256 `8c1ca3f71753e449db451c6ed33412bf9451bc4e2345f92b62a64b18e764e7cf`. Bounded ascending comment-ID reads, structural tombstones, authenticated same-origin creation, local selected-post state, abort/epoch guards and iterative tree construction were accepted. Post-integration `v2 CI` run `37339584913`, job `111862933910`, succeeded.

### Post voting

Exact executable `e1c5d1f65e72a81615164bc6445bcdc2d8218381`; exact-candidate CI run `37313953385`, success. Accepted package `m4-post-voting-20261005T133638Z.zip`, SHA-256 `4cf5c5839e7cfc427465821600bcf08a3bb56e9b5a22dbdead4fd185ca396b14`. PostgreSQL-authoritative explicit `-1/0/+1` state, post-row serialization, bounded viewer-vote reads and targeted optimistic frontend mutation were accepted. Post-integration `v2 CI` run `37321333276` succeeded.

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

- No unresolved correctness, SQL-plan, browser-performance or architecture blocker remains for tag mutations. Integration and post-fast-forward CI are the only remaining closure steps.
- No SQL/index/schema change is justified by the retained tag evidence. Revisit a numeric per-post tag cap/pagination only if future product requirements or measurements justify it.

## Single best next task

Non-force fast-forward the accepted `astra/m4-tag-mutations` history into the still-current `v2` ref, verify the resulting exact `v2` head with the normal `v2 CI`, record that integration result here, then begin the M4 profiles slice.