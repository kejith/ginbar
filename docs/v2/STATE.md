# Ginbar v2 state / handoff

Last updated: 2026-10-05
Phase: **M4 connected core product in progress; comment voting accepted and pending integration**
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
  - comment voting: **accepted after PostgreSQL/browser gate; integration pending**;
  - tag mutations and profiles remain.

## M4 comment voting — accepted, integration pending

Verified live GitHub `v2` base before this slice:

`06f5ab361448cf6578626f792304b39b4ee9c164`

Implementation branch:

`astra/m4-comment-voting`

Exact executable candidate:

`005b10ffa4b6f344d64ae6b0e9b5246a15527244`

Exact-candidate `v2 CI` run `37342739594`, job `111873754474`: **success**.

- exact SHA checkout/verification passed;
- scoped v2 correctness gate passed, including PostgreSQL-backed comment-vote concurrency/read tests and retained suites;
- frontend comment-vote helper tests, TypeScript/build checks and existing board/comment tests passed through the normal v2 gate;
- hermetic target-worker release build passed;
- tracked checkout remained clean.

### Accepted implementation boundary

- Authenticated explicit-state mutation is `PUT /api/v2/posts/:id/comments/:commentId/vote` with requested vote `-1`, `0` or `+1`; `0` removes the stored vote and repeated requested state is idempotent.
- A dedicated `commentvote` service/store boundary keeps vote mechanics separate from comment read/create semantics and keeps the HTTP handler thin.
- PostgreSQL remains authoritative. The mutation transaction validates a released/nondeleted post plus the target nondeleted comment belonging to that post, locks only the target comment row, reads the current user/comment vote, applies insert/update/delete, updates `comments.score` by `newVote - previousVote`, and commits the authoritative result.
- Missing, cross-post, deleted-comment and unavailable-post targets collapse to a non-votable not-found boundary. Deleted tombstones remain structural read nodes and never expose vote controls or non-neutral viewer state.
- Public bounded comment reads retain the accepted ascending immutable-ID cursor/order/tree contract. Signed-out reads use a separate neutral query shape; authenticated reads add bounded viewer-vote state without per-comment SQL.
- No schema/index change, Redis, write-behind, asynchronous score repair, global comment store or global vote store was introduced.
- Comment voting is optimistic. Clicking the active direction requests explicit neutral state; direct opposite-direction switches apply the exact ±2 optimistic score delta.
- Mutation sequencing is per comment, not global. Different comments can mutate independently; each comment retains last-confirmed authoritative score/vote, stale responses cannot overwrite newer intent, and the latest failed mutation restores confirmed state.
- Selection/post teardown aborts and invalidates in-flight vote work. Successful voting does not request feed, around or a replacement comment page merely to reconcile state.
- Targeted comment replacement preserves sibling comment objects; the bounded row cache preserves unrelated row identity where the keyed Solid path can reuse it.
- Existing comment pagination, parent IDs, tree order/depth, tombstones, orphan handling, delayed-read/create merge and iterative tree construction remain unchanged.

### Accepted correctness and PostgreSQL evidence

The exact-candidate suites cover all explicit transitions/idempotence, exact score deltas, multiple users, concurrent mixed voting, unavailable/cross-post/deleted targets, malformed IDs/payloads, 401/403 boundaries, viewer-vote pagination, tombstone neutrality, created-comment neutrality, optimistic helper behavior and retained v2 suites.

Accepted retained gate package:

`m4-comment-voting-20261005T170504Z.zip`

Independently validated SHA-256:

`481b990f4c3d4ac6a8f1e7b4b504733b7b80fef8d5e199e4d1ab74d2e234e356`

Archive integrity passed with 69 entries. Raw `findings.md`, SQL plans, DB consistency output, browser scenario snapshots, traces, request logs, CI metadata and cleanup evidence were inspected here. Exact tested executable was `005b10ffa4b6f344d64ae6b0e9b5246a15527244` in a clean detached worktree.

The realistic SQL fixture used 100,000 posts plus 200,000 comments and 200,000 comment-vote rows. `EXPLAIN (ANALYZE, BUFFERS)` established bounded/index-backed shapes:

- signed-out page: `posts_pkey` + `comments_post_idx`, 2 rows, 25 kB in-memory quicksort, **0.086 ms**;
- authenticated page: same bounded comment scan plus `comment_votes_user_idx`, 2 rows, 25 kB quicksort, **0.065 ms**;
- target validation/lock: `posts_pkey` + `comments_pkey`, 1 row, **0.076 ms**;
- current-vote lookup: `comment_votes_user_idx`, 1 row, **0.014 ms**;
- vote delete: indexed, **0.083 ms**;
- vote upsert: `comment_votes_pkey` conflict arbiter, **0.248 ms**;
- score update: `comments_pkey`, 1 row, **0.071 ms**.

No tested shape had a large-table sequential scan, unbounded sort, temp spill or N+1 query pattern. Existing indexes are sufficient; no new index is justified by the evidence. These timings establish boundedness/regression acceptance only, not a speedup claim.

Controlled score consistency passed all eight sequential transitions, neutral-row deletion, multi-user sums and concurrent mixed/flip rounds. In every controlled case `comments.score = base + SUM(stored votes)`.

### Accepted browser/DevTools evidence

The isolated real API/PostgreSQL/browser gate established:

- authenticated rows expose viewer vote correctly and all transition results match DOM, API and score state;
- active-direction click returns to neutral; opposite-direction flips reconcile exactly;
- a delayed mutation shows optimistic score/control state before response completion, then reconciles to authoritative success;
- a forced mutation failure rolls back to last confirmed score/vote and exposes visible error state;
- rapid same-comment actions finish at the last intended state without stale-response overwrite;
- independent comments are not globally serialized: the retained API snapshot shows comment 41 committed at `+1` while deliberately delayed comment 40 remained server-side neutral;
- load-more retains viewer-vote attachment by immutable comment ID;
- selection changes while a vote is pending do not inject state into the newly selected post; close/reopen re-synchronizes coherently;
- deleted tombstones expose no vote controls;
- signed-out reads remain usable/neutral; direct signed-out PUT returned 401 and cross-origin authenticated PUT returned 403, both with zero DB mutation;
- canonical `/post/:id`, search `q`, Back/Forward and Arrow/J/K navigation remain coherent;
- representative warmed voting generated exactly one vote PUT and **0 feed requests, 0 around requests and 0 comment-page GET reconciliation requests**;
- unrelated board thumbnails and a sibling comment retained DOM identity through a targeted vote;
- retained IDs remained descending/unique and within the 960-post bound;
- `window.__ginbarM4.assertInvariants()` passed and representative Long Tasks remained 0.

Observed anomalies were gate-script-only: a discarded score-regex parsing attempt and Playwright route-handler races were corrected and rerun. One retained overlap helper boolean remained false because it observed optimistic UI timing, while its own authoritative API snapshot proves the intended independent-comment overlap; this does not contradict application behavior. Expected signed-out `/me` 401 console logging and deliberate failure/abort traffic were separated from unexpected failures.

The isolated backend/nginx processes were stopped, both disposable databases were dropped, the detached worktree was removed, and canonical tracked status remained clean before and after. No prohibited tracked-source, SQL, docs, config, ref, deployment or persistent-state write occurred during the gate.

Decision: **accept M4 comment voting**. The mutation is concurrency-safe and PostgreSQL-authoritative, read/write SQL is bounded and indexed at realistic scale, optimistic/stale/failure behavior is correct, and existing board/comment/navigation/update-scope invariants remain intact. No additional cache, global store, index, polling/event stream or virtualization layer is justified.

Integration is still pending in this state snapshot. The accepted range must remain a pure non-force fast-forward from live `v2`, then post-fast-forward `v2 CI` must succeed before the slice is closed.

## M4 nested comments read/create — accepted and integrated

Exact executable candidate:

`d83ffa69c6a80418f13c5e4a2b5af19004846a51`

Exact-candidate `v2 CI` run `37334651076`, job `111846241798`: **success**.

Accepted evidence package:

`m4-comments-20261005T000000Z.zip`

SHA-256:

`8c1ca3f71753e449db451c6ed33412bf9451bc4e2345f92b62a64b18e764e7cf`

Accepted boundary: bounded public ascending comment-ID cursor reads; deleted structural tombstones; authenticated same-origin top-level/reply creation; exact UTF-8/Unicode body validation; one-statement validated PostgreSQL creation; no schema/index change; local selected-post state; abort/epoch guards; iterative tree construction; authoritative ID merge across delayed reads/creates. SQL plans used `comments_post_idx`, `comments_pkey` and released-post lookup without spill or comment-table sequential scan. Browser evidence established immediate shell behavior, nested/deleted tree correctness, authoritative create identity, delayed-read/create race safety, failure isolation, selection/navigation invariants, bounded update scope and signed-out/403 boundaries.

Accepted history was non-force fast-forwarded to `v2`; post-integration `v2 CI` run `37339584913`, job `111862933910`, succeeded. No blocker remains from this slice.

## M4 post voting — accepted and integrated

Exact executable candidate:

`e1c5d1f65e72a81615164bc6445bcdc2d8218381`

Exact-candidate CI run `37313953385`: **success**.

Accepted browser/SQL evidence package:

`m4-post-voting-20261005T133638Z.zip`

SHA-256:

`4cf5c5839e7cfc427465821600bcf08a3bb56e9b5a22dbdead4fd185ca396b14`

Post voting is PostgreSQL-authoritative, uses explicit `-1/0/+1` state, serializes competing score mutations with the post row lock, exposes viewer vote in bounded feed/around reads, and updates retained frontend post state optimistically without feed/around reloads or board-row remounts. The accepted browser gate covered transitions, rollback, rapid competing actions, history/search/navigation invariants, signed-out behavior and zero warmed feed/around refreshes.

The accepted post-voting head was fast-forwarded to `v2`, and post-integration `v2 CI` run `37321333276` succeeded. No blocker remains from this slice.

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

No correctness, SQL-plan, browser-performance or architecture blocker remains from the accepted comment-voting implementation. The only remaining gate for this slice is integration: pure fast-forward to live GitHub `v2` followed by successful `v2 CI` on the resulting integration SHA.

## Single best next task

Fast-forward the accepted `astra/m4-comment-voting` history into current GitHub `v2` without force, verify post-integration `v2 CI`, then update this state with the resulting integration SHA/run and begin the M4 tag-mutation slice.