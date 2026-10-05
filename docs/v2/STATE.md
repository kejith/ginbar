# Ginbar v2 state / handoff

Last updated: 2026-10-05
Phase: **M4 connected core product in progress; nested comments read/create implemented and CI/SQL-gated, browser acceptance pending**
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
  - nested comments read/create: **implemented, correctness/SQL-gated; browser acceptance pending**;
  - comment voting, tag mutations and profiles remain.

Remote GitHub `v2` remains authoritative. At the start of the nested-comment slice it was:

`3c7fcb9ed2f6815203bdd038c46d08e4ad2916fe` — `docs(v2): record integrated post voting`

The nested-comment work is isolated on `astra/m4-nested-comments`; neither `v2` nor `master` has been changed by this slice yet.

## M4 nested comments read/create — candidate awaiting browser acceptance

Feature branch: `astra/m4-nested-comments`, branched from `v2` at:

`3c7fcb9ed2f6815203bdd038c46d08e4ad2916fe`

Executable/test history:

- `511297ddfe700ca5d2f4565f481c588754fed73c` — `feat(v2): add nested comment read and create`;
- `cb645fbc60fbeb96e26ab228609c679951eea4fa` — `test(v2): retain comment SQL plan evidence`;
- `58675b286afcc18a3e1f72de2d5f8f55d2ce8a4f` — exact executable candidate; test-boundary hardening only.

Exact executable candidate:

`58675b286afcc18a3e1f72de2d5f8f55d2ce8a4f`

Exact-candidate `v2 CI` run `37333980699`: **success**.

The run checked out the exact SHA and completed the all-scope gate: Rust worker correctness, Go vet/tests with real PostgreSQL, frontend Node tests/typecheck/build, target-worker release-build verification and clean tracked-checkout verification. Frontend validation reported 28/28 tests passing, TypeScript `tsc --noEmit` passing and the Vite production build passing.

An earlier candidate run `37333164893` failed only because the SQL-plan test required `posts_pkey`; PostgreSQL correctly selected the smaller partial `posts_feed_released_idx` for the released-post lookup. The query itself was bounded/index-backed. The assertion was corrected to accept the justified released-post index and to retain full `EXPLAIN (ANALYZE, BUFFERS)` output. Backend follow-up run `37333626472` succeeded before the final all-scope run above.

### Candidate API and domain contract

- Public read endpoint: `GET /api/v2/posts/:id/comments?after=<comment-id>&limit=<n>`.
- Authenticated same-origin create endpoint: `POST /api/v2/posts/:id/comments` with `body` and optional numeric `parentCommentId`.
- Retrieval uses immutable comment-ID cursor pagination in ascending ID order; no `OFFSET` and no per-comment/N+1 SQL.
- Default page size is 100; maximum page size is 200. The store fetches `limit+1` rows to derive `nextAfter`.
- Ascending immutable-ID pagination starts at the oldest comment. Because a reply necessarily receives an ID after its parent, any reply returned on a later page has had its parent returned on the same or an earlier page; the cursor shape does not create detached replies.
- A valid released/nondeleted post with no comments returns an explicit empty comments array; unavailable/nonexistent posts return 404 semantics.
- Comment response identity uses immutable numeric `id`, `postId`, `authorId` and optional `parentCommentId`.
- No viewer comment-vote state exists in this slice and `comment_votes` is untouched.
- Deleted comments remain structural tombstones: identity, parent relation, score and timestamp remain available while body is omitted; existing descendants therefore retain hierarchy.
- Replying to a deleted parent is rejected as `409 parent_comment_deleted`.
- Missing and cross-post parents are both rejected as `404 parent_comment_not_found`; cross-post parent existence is not exposed.
- Comment bodies are not trimmed or rewritten. The service requires valid UTF-8 and 1–10,000 Unicode code points, matching the schema character-length boundary.
- Creation returns the authoritative inserted comment.

### PostgreSQL query shape

No schema migration or new index was added. Existing `comments_post_idx`, `comments_parent_idx`, post/comment primary keys and the released-post partial index are sufficient for the implemented query shapes.

Comment read is one bounded query:

- released/nondeleted post is established in the same SQL statement as the comment page;
- a bounded lateral subquery reads `comments` by `post_id`, `id > cursor`, `ORDER BY id ASC`, `LIMIT limit+1`;
- the valid-post/empty-comments case is distinguishable without a redundant post-existence round trip.

Comment creation is one data-modifying CTE:

- target released/nondeleted post is validated and `FOR SHARE` locked;
- optional parent is looked up by immutable comment primary key and `FOR SHARE` locked;
- parent status distinguishes valid, missing/cross-post and deleted;
- insertion happens only from the validated CTE and returns the authoritative row;
- there is no read-then-write validation race or extra application round trip.

### SQL-plan evidence retained by CI

`TestCommentSQLPlansStayBoundedAndIndexed` seeds realistic competing data before running `EXPLAIN (ANALYZE, BUFFERS)`: 5,000 unrelated post rows, 20,000 comments on a noise post and 20,000 comments on the target post.

Final green-plan evidence establishes:

- comment read uses `posts_feed_released_idx` and `comments_post_idx`;
- read work is bounded to 101 comment rows for the tested page (`limit+1`);
- only a small in-memory sort is present for the bounded result; no external/temp spill;
- creation validation uses `posts_feed_released_idx` and `comments_pkey`;
- no large-table sequential scan on `comments` is attributable to the new API;
- no unbounded sort or temp spill is present;
- execution was sub-millisecond in the CI fixture (approximately 0.28 ms read and 0.63 ms create in the retained final run), which is boundedness evidence only and not a speedup claim.

### Correctness coverage

The exact green candidate covers at minimum:

- empty valid post comment list versus unavailable/unreleased post;
- deterministic ascending comment ordering;
- cursor/page boundaries and `nextAfter`;
- multiple nesting levels and siblings;
- deleted-parent tombstone preservation;
- top-level authenticated creation;
- nested reply creation;
- authoritative returned post/user/parent/body/score identity;
- nonexistent parent;
- cross-post parent rejection;
- deleted-parent reply rejection;
- invalid/nonexistent post;
- signed-out create rejection;
- cross-origin create rejection before mutation;
- malformed post IDs, cursor/limit values, JSON, unknown fields and payloads;
- Unicode body boundary: 10,000 code points accepted, 10,001 rejected;
- no comment-vote viewer state added;
- 20,000-level frontend nesting projection without recursive traversal;
- child-before-parent input still attaches correctly in the tree helper;
- existing auth/feed/search/post-vote/media/worker suites remain green.

### Frontend ownership and rendering

- Comment server state belongs to the selected expanded-post experience only; it is not added to the retained 960-post board array or a global comment store.
- `ExpandedPost` keeps its existing media/meta/vote/media-status behavior and mounts one `Comments` child beneath it.
- Comment reads start after the expanded shell is mounted; selection does not await comment I/O.
- Read and create requests use `AbortController` plus post/epoch guards, preventing stale responses from injecting into a newly selected/reopened post.
- A one-pass `id -> comment` and `parent -> children` index builds the visible tree; iterative stack traversal avoids recursive rendering limits and repeated whole-list rescans.
- Missing-parent rows are surfaced explicitly rather than silently reparented; under the API cursor invariant they should not occur during normal loading.
- Creation is deliberately non-optimistic: submit waits for the authoritative server row, then merges it by immutable ID into visible comment state.
- Top-level creation and reply creation share a bounded composer. Deleted/orphan rows do not expose reply controls.
- Signed-out users can read comments; create/reply UI is unavailable with a sign-in note.
- Comment activity does not mutate the retained post object and should not require feed/around refreshes.

### Browser acceptance still required before integration

Do not integrate this candidate until isolated browser/DevTools evidence establishes:

- selecting a post shows the expanded shell immediately while comment retrieval is deliberately delayed;
- multiple nested levels and siblings render under the correct parents;
- a top-level create appears from the authoritative response;
- a reply appears under the correct parent;
- failed creation shows a clear error and does not corrupt the visible tree;
- changing selection while a comment read is pending cannot inject the old post's comments into the new post;
- closing and reopening is coherent;
- Back/Forward and Arrow/J/K navigation remain coherent;
- searched-board `q` remains unchanged through comment activity;
- comment reads/creates cause no feed/around request merely to refresh an already-retained post;
- comment updates do not remount unrelated board rows;
- Long Task/update-scope evidence remains acceptable;
- retained post count/order/uniqueness and the 960-post cap remain unchanged;
- signed-out comment reads work and signed-out create/reply controls remain unavailable.

Decision so far: **implementation and SQL/correctness gate accepted; browser acceptance pending**. No cache layer, speculative index, virtualization, comment voting or frontend state framework is justified by current evidence.

## M4 post voting — accepted and integrated

Post voting remains closed and integrated. Accepted executable candidate:

`e1c5d1f65e72a81615164bc6445bcdc2d8218381`

Accepted feature head integrated into `v2`:

`c0469996b7c3a7385f2d8796b65ec12cf6e8ba6d`

Exact-candidate CI run `37313953385`: **success**. Post-fast-forward `v2 CI` run `37321333276`: **success**.

Accepted evidence package:

`m4-post-voting-20261005T133638Z.zip`

SHA-256:

`4cf5c5839e7cfc427465821600bcf08a3bb56e9b5a22dbdead4fd185ca396b14`

The accepted boundary remains: authenticated same-origin `PUT /api/v2/posts/:id/vote`, PostgreSQL-authoritative transaction with post-row locking, explicit `-1/0/+1` state, feed/around viewer vote in the existing bounded query, optimistic retained-post-only UI state, per-post ordered mutation queues and no feed/around refresh for voting. Browser evidence established all vote transitions, rollback on failure, rapid-action ordering, signed-out semantics, search/history/navigation coherence, zero warmed feed/around refreshes, zero unrelated row mount/unmount activity and retained-state invariants.

## Retained M4 board/auth/search decisions

Keep these accepted boundaries unless new evidence requires change:

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

Do not pull these into the current comment-read/create gate:

- comment voting;
- tag mutations or profiles;
- moderation/admin or uploads;
- Redis synchronization, WebSockets/event streams or a large frontend store;
- virtualization without evidence;
- broader video transcoding or exact WebM/EBML acceptance;
- media orphan/janitor hardening;
- deployment UID/GID/media-storage permissions;
- stronger filesystem hardening if the media-tree threat model changes;
- auth abuse/rate limits and KDF admission controls before production-hardening evidence requires them;
- v1-v2 end-to-end speedup claims before an apples-to-apples benchmark exists.

## Unresolved issues

The nested comment candidate has no known correctness or SQL-plan blocker. **Browser/DevTools acceptance is the remaining gate before integration.**

## Single best next task

Run the isolated **M4 nested comments browser/DevTools acceptance gate** against exact executable `58675b286afcc18a3e1f72de2d5f8f55d2ce8a4f`, retain raw evidence in one `.local-agent-results/` ZIP, inspect that evidence here, then either fix/re-gate any defect or record acceptance and prepare a reviewed non-force fast-forward into `v2`. Keep comment voting out until this read/create slice is accepted and integrated.
