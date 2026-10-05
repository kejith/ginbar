# Ginbar v2 state / handoff

Last updated: 2026-10-05
Phase: **M4 connected core product in progress; tag-mutation implementation candidate awaiting SQL/browser gate**
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
  - tag mutations: **implementation candidate complete; exact-candidate CI green; external SQL/browser gate pending**;
  - profiles remain afterward.

## M4 tag mutations — implementation candidate; acceptance gate pending

Verified live GitHub `v2` base before this slice:

`e7da7d7ed80599cffd0caf9c32e0db586366265a`

Implementation branch:

`astra/m4-tag-mutations`

Exact executable candidate:

`a5ff30597efd718f7ab5b6488ebc9c762197fff1`

Exact-candidate `v2 CI` run `37356154056`, job `111918951457`: **success**.

- exact SHA checkout/verification passed;
- scoped v2 correctness gate passed, including the new PostgreSQL-backed tag mutation tests and retained v2 suites;
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
- Frontend tag state is local to the expanded post and authoritative-first. Same-row selection changes abort/invalidate old load/mutation work by post ID plus epoch; cross-row/unmount cleanup aborts in-flight work. Old-selection or mismatched-post responses cannot update the newly selected post.
- The board shell still updates immediately on selection; tag loading is independent of shell rendering. No large global store or whole-board replacement was introduced.

### Candidate correctness coverage

The exact-candidate suites cover:

- normalization and invalid/unsearchable tag names;
- authenticated add and unauthenticated rejection;
- repeated/idempotent add and soft-removed relation reactivation;
- missing/deleted post rejection without orphan tag creation;
- moderator/admin removal and ordinary-user rejection with zero mutation;
- deterministic absent-relation removal;
- concurrent same-tag creation/add attempts with one tag entity/relation;
- concurrent add/remove consistency without duplicate relations or partial removal metadata;
- immediate reuse of existing include-tag search semantics after add/remove;
- signed-out/ordinary/moderator selected-post capability state;
- same-origin rejection before mutation;
- authoritative HTTP response state;
- stale-selection/epoch/aborted-response frontend isolation;
- retained auth/feed/search/post-vote/comment/comment-vote suites via normal v2 CI.

### Pending acceptance gate

Do **not** integrate this slice into `v2` yet. The exact executable candidate above needs the normal isolated local-agent evidence gate before acceptance.

Required PostgreSQL evidence at realistic scale should cover `EXPLAIN (ANALYZE, BUFFERS)` for the actual new hot shapes, including:

- signed-out selected-post tag read;
- authenticated selected-post tag read and role capability lookup;
- eligible-post lock/validation;
- normalized tag lookup and create/conflict path;
- post/tag relation insert/reactivation;
- moderator/admin authorization and active relation removal;
- existing include/exclude feed search after realistic tag/post-tag population.

Confirm there is no attributable large-table sequential scan, unbounded large-table sort, temp spill or N+1 pattern and that existing constraints/indexes are sufficient. The selected-post read is keyed to one post, but the schema does not impose an explicit numeric per-post tag-count cap; validate realistic tag cardinality and only introduce a product cap/pagination/index if evidence shows it is necessary. Do not invent a speedup claim.

Required browser/real-API evidence should cover at minimum:

- selected-post shell remains immediate while tag state loads independently;
- signed-in ordinary user can add and receives authoritative normalized/idempotent state;
- moderator/admin remove works and ordinary-user/signed-out/cross-origin remove/add failures produce zero unauthorized DB mutation;
- failed mutations keep/reconcile the last authoritative tag snapshot and expose a usable error;
- same-row and cross-row selection changes while tag reads/mutations are delayed do not leak stale state;
- successful add/remove causes no feed/around reconciliation request when the tag response is sufficient;
- unrelated thumbnail rows/DOM identity, route/search/Back/Forward/Arrow/J/K behavior and existing post/comment UI remain coherent;
- retained IDs remain descending/unique/within the board bound and `window.__ginbarM4.assertInvariants()` passes;
- representative tag interaction does not introduce Long Tasks.

Retain raw plans, DB state/results, browser snapshots/traces/request logs, exact CI metadata and cleanup evidence in the standard local-agent ZIP. Any executable change after this gate invalidates the gate and requires green CI on the new exact SHA before rerunning acceptance evidence.

Decision: **implementation is ready for the isolated SQL/browser gate, but tag mutations are not yet accepted or integrated**.

## M4 comment voting — accepted and integrated

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

### Integration verification

Accepted history was non-force fast-forwarded on remote `v2`:

`06f5ab361448cf6578626f792304b39b4ee9c164 -> 3c1731208fca815eac6d3e189f4951862fa98b1c`

The range was a pure fast-forward and contained the exact executable candidate plus documentation-only state commits. No merge commit or force update was used.

Post-fast-forward `v2 CI` run `37351495807`, job `111903167789`: **success**.

- `head_branch=v2`;
- `head_sha=3c1731208fca815eac6d3e189f4951862fa98b1c`;
- exact checkout and SHA verification succeeded;
- scoped v2 correctness gate succeeded;
- target-worker release-build verification succeeded;
- tracked checkout remained clean;
- all job steps completed successfully.

Integration decision: **comment voting is fully integrated and the M4 slice is closed**.

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

- Tag mutations are not yet accepted: realistic-scale PostgreSQL plans and isolated browser/real-API behavior still require retained evidence against exact executable `a5ff30597efd718f7ab5b6488ebc9c762197fff1`.
- The selected-post tag query is constrained by immutable post ID and indexed relations but there is no explicit numeric per-post tag-count cap in the current schema. Validate realistic cardinality in the gate; only add a cap/pagination/index if evidence or product requirements justify it.

No unresolved correctness, SQL-plan, browser-performance, integration or architecture blocker remains from the accepted comment-voting slice.

## Single best next task

Run the isolated **M4 tag-mutation SQL/browser acceptance gate** against exact executable `a5ff30597efd718f7ab5b6488ebc9c762197fff1` after confirming exact-candidate CI run `37356154056` / job `111918951457` remains green. Use realistic tag/post-tag scale, capture `EXPLAIN (ANALYZE, BUFFERS)` for the new read/write shapes, exercise real API/browser add/remove/auth/stale-selection/update-scope behavior, retain the standard evidence ZIP, and inspect that evidence here before any integration into `v2`.