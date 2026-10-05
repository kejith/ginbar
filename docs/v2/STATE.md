# Ginbar v2 state / handoff

Last updated: 2026-10-05
Phase: **M4 connected core product in progress; post voting accepted, browser/SQL-gated and integrated**
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
  - nested comments read/create, comment voting, tag mutations and profiles remain.

## M4 post voting — accepted and integrated

Feature branch: `astra/m4-post-voting`, branched from GitHub `v2` at:

`a1043990f4deafb4ba19a84877fdd61fe015236e`

Reviewed feature history:

- `146e6afc3d9b1afde122728814ffbacf9aded5a1` — `feat(v2): add authenticated post voting`
- `e1c5d1f65e72a81615164bc6445bcdc2d8218381` — exact executable candidate; test-fixture correction only
- `9b584bd937c7bcc812a10ed7509b7f73c5569cd7` — documentation-only candidate record
- `c0469996b7c3a7385f2d8796b65ec12cf6e8ba6d` — accepted gate/state record and integrated head

Exact executable candidate:

`e1c5d1f65e72a81615164bc6445bcdc2d8218381`

Exact-candidate CI run `37313953385`: **success**. It matched the executable SHA and completed the scoped v2 correctness gate, PostgreSQL-backed Go tests, frontend validation/tests/build, worker checks, target-worker release-build verification and clean tracked-checkout verification.

An earlier executable `146e6afc3d9b1afde122728814ffbacf9aded5a1` failed CI run `37313602695` only because two PostgreSQL test fixture usernames exceeded the existing username-length constraint. Production code did not change; the bounded fixture correction produced the accepted executable above.

### Accepted implementation boundary

- `PUT /api/v2/posts/:id/vote` is an authenticated same-origin mutation with explicit requested state `-1`, `0` or `+1`.
- PostgreSQL remains authoritative; no Redis/cache/write-behind voting path was introduced.
- The mutation is one transaction: lock the released/nondeleted post row, read the user's current vote after that lock, delete/upsert `post_votes`, derive score delta as `newVote-currentVote`, update `posts.score`, and commit.
- The post row lock serializes competing score mutations on one post. Existing vote indexes and constraints are reused; no schema migration was needed.
- Repeating an explicit requested state is idempotent. The frontend maps clicking the active direction to neutral.
- Feed and around resolve optional viewer identity once through the existing session boundary and expose `userVote` in the same bounded query. Signed-out reads return literal neutral state without joining `post_votes`.
- Optimistic UI updates mutate only the retained post state. Expanded state continues to derive from that same retained object rather than a duplicate post copy.
- Per-post mutation queues preserve server request order; sequence numbers prevent stale responses from overwriting newer intent.
- Latest failures roll back to the last confirmed authoritative state; a 401 also transitions the UI auth state to signed out.
- Voting a retained post does not trigger feed/around reloads and does not alter search `q`, canonical `/post/:id`, Back/Forward, Arrow/J/K navigation, stable row identity or the 960-post retention architecture.
- Comment voting, tag mutations, comments, profiles, moderation/admin, uploads and Redis remain out of this slice.

### Correctness coverage

Green exact-candidate coverage includes:

- all six transitions: `0 -> +1`, `0 -> -1`, `+1 -> 0`, `-1 -> 0`, `+1 -> -1`, `-1 -> +1`, with exact score deltas;
- repeated explicit-state idempotence;
- nonexistent post;
- unauthenticated and cross-origin mutation rejection;
- malformed, missing, null and out-of-range vote values;
- two users voting on one post;
- concurrent mixed mutations with `posts.score == baseScore + SUM(post_votes.value)`;
- authenticated feed/around returning viewer vote;
- signed-out feed/around returning neutral viewer vote;
- retained auth/search/feed/around/media/worker suites.

## Accepted SQL/browser gate

Evidence package:

`m4-post-voting-20261005T133638Z.zip`

Independently validated SHA-256:

`4cf5c5839e7cfc427465821600bcf08a3bb56e9b5a22dbdead4fd185ca396b14`

Archive integrity passed. The package contains raw SQL plans, browser traces/results, CI metadata, commands, versions and cleanup proof. Exact tested executable: `e1c5d1f65e72a81615164bc6445bcdc2d8218381`.

### SQL-plan evidence

Disposable fixture: 100,000 posts, 100,000 media rows, 200,000 post-vote rows, 1,000 users.

- authenticated first feed page: 61 rows, execution **0.188 ms**, index-backed `posts_pkey` + `media_pkey` + `post_votes_user_idx`;
- authenticated old-cursor feed: 61 rows, execution **0.136 ms**, same bounded/index-backed shape with `id < 50000`;
- authenticated around post 49999: 61 rows, execution **0.577 ms**; only 25–28 kB in-memory quicksorts, no spill; posts/media/votes remain index-backed;
- post-row lock: `posts_pkey`, execution **0.016 ms**;
- current-vote lookup: `post_votes_user_idx`, execution **0.005 ms**;
- upsert conflict arbiter: `post_votes_pkey`, execution **0.146 ms**;
- score update: `posts_pkey`, execution **0.119 ms**;
- no large-table sequential scans, temp spills or unbounded row growth attributable to viewer vote state.

These numbers establish boundedness/regression acceptance only; they are not a performance-improvement claim.

### Browser evidence

The isolated browser/API/nginx/PostgreSQL gate established:

- all six vote transitions correct in DOM and database;
- optimistic change visible about 250 ms after click while a deliberately delayed 1,200 ms response was pending, followed by authoritative reconciliation;
- intentional HTTP 500 mutation failure rolled back to the last confirmed state and surfaced an error;
- rapid up-then-down produced two mutation requests and ended at the last intent without stale-response corruption;
- vote state survived select-away-and-return;
- searched-board voting preserved `q=score:>=35`;
- Back/Forward and Arrow/J/K remained coherent;
- canonical `/post/40` path/query remained unchanged by voting;
- authenticated feed and around exposed the current viewer vote;
- signed-out reads returned neutral `userVote`, signed-out mutation returned 401 without score change, and vote buttons were disabled;
- warmed voting added **0 feed requests / 0 around requests**, **0 row mounts / 0 row unmounts**, and Long Tasks remained **0 -> 0**;
- retained state stayed ordered/unique and within the 960-post bound.

One raw browser check initially reported `expandedScore=4041`; this was a harness regex artifact that concatenated `#40` and `41 points`. The same raw record contained coherent score fields and a dedicated re-probe confirmed expanded/thumbnail equality (`40 == 40`). No application defect was found.

The acceptance runtime/databases/worktree were cleaned up and the canonical tracked checkout was clean. No production data/state was used.

Decision: **accept M4 post voting**. The SQL shapes are bounded/index-backed, mutation/concurrency coverage is sufficient, optimistic/failure/rapid-action behavior is correct, and the frontend preserves accepted board update-scope invariants. No additional cache layer, index, frontend store or architecture change is justified by the evidence.

## Integration verification

The accepted head was non-force fast-forwarded on remote `v2`:

`a1043990f4deafb4ba19a84877fdd61fe015236e -> c0469996b7c3a7385f2d8796b65ec12cf6e8ba6d`

Integration evidence package:

`m4-integration-20261005T140027Z.zip`

Independently validated SHA-256:

`b1b73e6848a92813ed7f947e3a0de29b733e6486ea182ad9b91957980a58dab8`

Archive integrity passed with 22 entries. Raw evidence confirms:

- immediately before the write, remote `v2` was exactly `a1043990f4deafb4ba19a84877fdd61fe015236e`;
- `merge-base --is-ancestor` old `v2` -> accepted head returned exit 0;
- the range contained exactly the four reviewed commits `146e6af`, `e1c5d1f`, `9b584bd`, `c046999`;
- exact push command was `git push origin c0469996b7c3a7385f2d8796b65ec12cf6e8ba6d:refs/heads/v2`;
- push exited 0 as a normal non-force fast-forward;
- post-push remote `v2` resolved exactly to `c0469996b7c3a7385f2d8796b65ec12cf6e8ba6d`;
- no other ref, tracked source/docs/SQL/config, commit, tag, deployment or persistent application/database state was modified by the integration agent;
- canonical tracked status was clean after the operation.

Post-fast-forward `v2 CI` run `37321333276`: **success**.

- `head_branch=v2`;
- `head_sha=c0469996b7c3a7385f2d8796b65ec12cf6e8ba6d`;
- event `push`;
- job `correctness` (`111800855878`) succeeded;
- all eight job steps succeeded, including exact checkout, scoped v2 correctness gate, target worker release-build verification and clean tracked-checkout verification.

Remote GitHub `v2` is authoritative. The integration agent intentionally left its unrelated local `refs/heads/v2` divergence untouched.

Integration decision: **post voting is fully integrated and the M4 post-voting slice is closed**.

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

Do not pull these into the next slice without a concrete requirement:

- comment voting before comment read/create exists on v2;
- broader video transcoding or exact WebM/EBML acceptance;
- media orphan/janitor hardening;
- deployment UID/GID/media-storage permissions;
- stronger filesystem hardening if the media-tree threat model changes;
- auth abuse/rate limits and KDF admission controls before production-hardening evidence requires them;
- virtualization, Redis synchronization, event streams or a large frontend store;
- v1-v2 end-to-end speedup claims before an apples-to-apples benchmark exists.

## Unresolved issues

No unresolved correctness, SQL-plan, browser-performance or integration blocker remains from the post-voting slice.

## Single best next task

Begin the **M4 nested comments read/create slice** from current GitHub `v2`. Add bounded/indexed retrieval for the selected post using the existing `comments(post_id, id)` and parent indexes, authenticated comment creation using immutable numeric user/post/parent IDs with strict parent-post validation, and nested SolidJS rendering that preserves immediate post selection, search/history/navigation, stable board rows and the 960-post retention boundary. Keep comment voting out of this slice; add it only after the v2 comment read/create path is accepted and browser-gated.
