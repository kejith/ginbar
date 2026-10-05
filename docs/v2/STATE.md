# Ginbar v2 state / handoff

Last updated: 2026-10-05
Phase: **M4 connected core product in progress; nested comments read/create accepted, browser/SQL-gated and integrated**
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
  - comment voting, tag mutations and profiles remain.

## M4 nested comments read/create — accepted and integrated

Verified GitHub `v2` base:

`3c7fcb9ed2f6815203bdd038c46d08e4ad2916fe`

Exact executable candidate:

`d83ffa69c6a80418f13c5e4a2b5af19004846a51`

Gate branch:

`astra/m4-nested-comments-gate`

Exact-candidate `v2 CI` run `37334651076`, job `111846241798`: **success**.

- exact SHA checkout/verification passed;
- full `scope=all` correctness gate passed, including PostgreSQL-backed backend tests and retained worker suites;
- frontend 27-test suite, `tsc --noEmit` and production Vite build passed;
- hermetic target-worker release build passed;
- tracked checkout remained clean.

### Accepted implementation boundary

- Public comment read endpoint is `GET /api/v2/posts/:id/comments` with immutable ascending comment-ID cursor `after` and bounded `limit`; default 100, maximum 200. No OFFSET pagination or per-comment query is used.
- The read store uses one released/nondeleted-post query with a bounded lateral comment scan. A valid post with zero comments is distinguishable from an unavailable post without a second existence round trip.
- Rows are deterministic by ascending immutable comment ID. Replies cannot precede their existing parent identity.
- Deleted comments remain structural tombstones: immutable IDs/parent identity stay visible while body is omitted, preserving descendants.
- Authenticated creation is same-origin `POST /api/v2/posts/:id/comments` using immutable numeric `users.id`; signed-out mutation is rejected.
- Body is preserved exactly and validated as valid UTF-8 with 1–10,000 Unicode code points. Request bytes and unknown JSON fields are bounded/rejected.
- Top-level and reply creation use one PostgreSQL statement. Materialized CTEs lock/validate the released/nondeleted post and optional parent with `FOR SHARE`, reject missing/cross-post parents, reject replies to deleted parents, insert only after validation, and return the authoritative row.
- No schema/index change was needed. Existing `comments_post_idx`, `comments_pkey` and the released-post index cover measured query shapes.
- Comment state stays local to the selected expanded-post experience; it is not attached to the retained board post array and no global comment store/cache was introduced.
- Comment GET/POST requests are abortable and epoch-guarded across selection/close/route changes. Selection still renders the expanded shell before comment network work completes.
- Tree construction builds parent/children maps once and uses iterative preorder traversal; tests cover 20,000 nesting levels without recursive traversal.
- Initial and incremental GET paths merge authoritative rows by immutable ID, preventing a delayed initial read from erasing a newer successful create.
- Comment voting remains outside this slice.

### Correctness and SQL-plan evidence

Green exact-candidate coverage includes empty-vs-unavailable posts, deterministic ordering/cursors, nested siblings/depth, deleted-parent tombstones, authenticated top-level/reply creation, authoritative returned identity, invalid/cross-post/deleted parents, signed-out/cross-origin rejection, malformed inputs, Unicode body boundaries, explicit orphan handling, iterative 20,000-level traversal, and retained auth/feed/search/post-vote/media/worker suites.

`EXPLAIN (ANALYZE, BUFFERS)` on the CI fixture established bounded/index-backed shapes:

- comment read: `posts_feed_released_idx` + `comments_post_idx`, `LIMIT 101`, 32 kB in-memory quicksort, execution **0.176 ms**, no comment-table sequential scan or spill;
- reply creation: `posts_feed_released_idx` + `comments_pkey`, one-row validated insert, execution **0.652 ms**, no comment-table sequential scan or spill.

These measurements establish boundedness/regression acceptance only; they are not a performance-improvement claim.

### Accepted browser/DevTools gate

Evidence package:

`m4-comments-20261005T000000Z.zip`

Independently validated SHA-256:

`8c1ca3f71753e449db451c6ed33412bf9451bc4e2345f92b62a64b18e764e7cf`

Archive integrity passed with 20 entries. Raw findings, browser step snapshots, network logs, traces, HTTP responses, DB verification and runtime/cleanup metadata were inspected here. Exact tested executable: `d83ffa69c6a80418f13c5e4a2b5af19004846a51`.

The isolated browser/API/PostgreSQL gate established:

- delayed comment GET did not delay the selected expanded shell, media element or post metadata; releasing it loaded 100 comments plus pagination;
- nested preorder, sibling order, depths, deleted-parent tombstone and surviving descendants were correct; after page 2 there were 121 rows and zero orphan warnings;
- authenticated top-level and reply creates returned authoritative rows, appeared exactly once, and matched DB post/user/parent/body/score identity; zero cross-post parent relationships were present;
- the delayed-read/create race was correct: a successful create completed while the initial GET was held, and the stale GET later merged without erasing or duplicating the created comment;
- an intentional HTTP 500 create failure surfaced a `role="alert"` error, left visible state intact and inserted no DB row;
- a held post-1 response could not leak into post 2 after selection changed; close/reopen remained coherent;
- canonical `/post/:id`, search `q`, Back/Forward, Arrow/J/K navigation, descending/unique retained IDs and the 960-post retention bound remained coherent; `window.__ginbarM4.assertInvariants()` passed;
- representative warmed comment activity generated **0 feed requests, 0 around requests and 0 vote requests**;
- unrelated board row mounts remained **1 -> 1**, unmounts **0 -> 0**, and Long Tasks remained **0 -> 0** around representative comment creation;
- signed-out comment GET remained usable while composer/reply controls were absent; direct signed-out POST returned 401 and cross-origin authenticated POST returned 403, both with zero DB mutation;
- no comment-vote controls, requests, routes or viewer state were introduced.

Expected-only browser noise was observed: one intentionally aborted stale comment GET, one forced 500, and two expected 401 console entries. The gate did not provision media bytes, so `/media/*` fell back to the SPA; this does not block this comments gate because the required immediate-shell assertion concerns element/src/meta availability rather than media decoding correctness.

The temporary API/proxy processes, disposable schema and detached worktree were removed. Canonical tracked checkout remained clean; no prohibited source/SQL/docs/config/ref/deployment or persistent-state writes occurred during the browser gate.

Decision: **accept M4 nested comments read/create**. The implementation is bounded and indexed, browser behavior preserves existing board/navigation/update-scope invariants, stale-request and delayed-read/create races are handled correctly, and no additional cache, global store, index or virtualization layer is justified by the evidence.

### Integration verification

Accepted history was non-force fast-forwarded on remote `v2`:

`3c7fcb9ed2f6815203bdd038c46d08e4ad2916fe -> 19cacdd9999e2af08767942fa9d71bc2dbd5d5e6`

The range was a pure fast-forward and contained the exact executable plus documentation-only gate/state commits. No merge commit or force update was used.

Post-fast-forward `v2 CI` run `37339584913`, job `111862933910`: **success**.

- `head_branch=v2`;
- `head_sha=19cacdd9999e2af08767942fa9d71bc2dbd5d5e6`;
- exact checkout and SHA verification succeeded;
- scoped correctness gate succeeded;
- target-worker release-build verification succeeded;
- tracked checkout remained clean;
- all job steps completed successfully.

Integration decision: **nested comments read/create is fully integrated and the M4 slice is closed**.

## M4 post voting — accepted and integrated

Exact executable candidate:

`e1c5d1f65e72a81615164bc6445bcdc2d8218381`

Exact-candidate CI run `37313953385`: **success**.

Accepted browser/SQL evidence package:

`m4-post-voting-20261005T133638Z.zip`

SHA-256:

`4cf5c5839e7cfc427465821600bcf08a3bb56e9b5a22dbdead4fd185ca396b14`

Post voting is PostgreSQL-authoritative, uses explicit `-1/0/+1` state, serializes competing score mutations with the post row lock, exposes viewer vote in bounded feed/around reads, and updates retained frontend post state optimistically without feed/around reloads or board-row remounts. The accepted browser gate covered all transitions, failure rollback, rapid competing actions, history/search/navigation invariants, signed-out behavior and zero warmed feed/around refreshes.

The accepted post-voting head was fast-forwarded to `v2`, and post-integration `v2 CI` run `37321333276` succeeded. No unresolved blocker remains from that slice.

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

No unresolved correctness, SQL-plan, browser-performance, integration or architecture blocker remains from the nested comments read/create slice.

## Single best next task

Begin the **M4 comment voting** slice from current GitHub `v2`. Add authenticated explicit-state comment voting with PostgreSQL-authoritative score consistency and bounded/indexed viewer-vote reads, then connect it to the existing nested comment UI without introducing feed/around reloads, board-row remounts, a global comment store, Redis, tag mutations or profiles. Preserve the accepted comments pagination/tree/stale-request behavior and browser-gate the slice before integration.
