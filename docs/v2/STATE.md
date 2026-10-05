# Ginbar v2 state / handoff

Last updated: 2026-10-05
Phase: **M4 connected core product in progress; post voting accepted, integration fast-forward pending only because the GitHub ref-update connector cannot update top-level `v2`**
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
  - post voting: **accepted; feature history ready for fast-forward into `v2`**;
  - comment voting, tag mutations, nested comments and profiles remain.

## M4 post voting — accepted

Feature branch: `astra/m4-post-voting`, branched from GitHub `v2` at:

`a1043990f4deafb4ba19a84877fdd61fe015236e`

Reviewed history before this state-only update:

- `146e6afc3d9b1afde122728814ffbacf9aded5a1` — `feat(v2): add authenticated post voting`
- `e1c5d1f65e72a81615164bc6445bcdc2d8218381` — exact executable candidate; test-fixture correction only
- `9b584bd937c7bcc812a10ed7509b7f73c5569cd7` — documentation-only candidate record

Exact executable candidate:

`e1c5d1f65e72a81615164bc6445bcdc2d8218381`

Exact-candidate CI run `37313953385`: **success**. The run matched the exact SHA and completed the scoped v2 correctness gate, PostgreSQL-backed Go tests, frontend validation/tests/build, worker checks, target-worker release-build verification and clean tracked-checkout verification.

An earlier executable `146e6afc3d9b1afde122728814ffbacf9aded5a1` failed CI run `37313602695` only because two PostgreSQL test fixture usernames exceeded the existing username-length constraint. Production code did not change; the bounded fixture correction produced the accepted executable above.

### Accepted implementation boundary

- `PUT /api/v2/posts/:id/vote` is an authenticated same-origin mutation with explicit requested state `-1`, `0` or `+1`.
- PostgreSQL remains authoritative; no Redis/cache/write-behind voting path was added.
- The mutation is one transaction: lock the released/nondeleted post row, read the user's current vote after that lock, delete/upsert `post_votes`, derive score delta as `newVote-currentVote`, update `posts.score`, and commit.
- The post row lock serializes competing score mutations on one post; existing vote indexes/constraints are reused with no schema migration.
- Repeating an explicit requested state is idempotent. The frontend maps clicking the active direction to neutral.
- Feed and around resolve optional viewer identity once through the existing session boundary and expose `userVote` in the same bounded query; signed-out reads return literal neutral state without joining `post_votes`.
- Optimistic UI updates mutate only the retained post object. Expanded state continues to derive from the same retained post state, not a duplicate copy.
- Per-post mutation queues preserve server request order and sequence numbers prevent stale responses from overwriting newer intent.
- Latest failures reconcile to the last confirmed authoritative state. A 401 transitions auth UI state to signed out.
- Voting a retained post does not trigger feed/around reloads and does not change search `q`, canonical `/post/:id`, Back/Forward, Arrow/J/K navigation, stable row identity, or the 960-post retention architecture.
- Comment voting, tag mutations, comments, profiles, moderation/admin, uploads and Redis remain out of this slice.

### Correctness coverage

Green candidate coverage includes:

- all six transitions: `0 -> +1`, `0 -> -1`, `+1 -> 0`, `-1 -> 0`, `+1 -> -1`, `-1 -> +1`, with exact score deltas;
- repeated explicit state idempotence;
- nonexistent post;
- unauthenticated and cross-origin mutation rejection;
- malformed, missing, null and out-of-range vote values;
- two users voting on one post;
- concurrent mixed mutations with `posts.score == baseScore + SUM(post_votes.value)`;
- authenticated feed/around returning viewer vote;
- signed-out feed/around returning neutral viewer vote;
- retained existing auth/search/feed/around/media/worker suites.

## Accepted SQL/browser gate

Evidence package:

`m4-post-voting-20261005T133638Z.zip`

Independently validated SHA-256:

`4cf5c5839e7cfc427465821600bcf08a3bb56e9b5a22dbdead4fd185ca396b14`

Archive integrity check passed. The package contains raw SQL plans, browser traces/results, CI metadata, commands, versions and cleanup proof. The exact tested executable is `e1c5d1f65e72a81615164bc6445bcdc2d8218381`.

### SQL-plan evidence

Disposable fixture size: 100,000 posts, 100,000 media rows, 200,000 post-vote rows, 1,000 users.

- authenticated first feed page: 61 rows, execution **0.188 ms**, index-backed `posts_pkey` + `media_pkey` + `post_votes_user_idx`;
- authenticated old-cursor feed: 61 rows, execution **0.136 ms**, same bounded/index-backed shape with `id < 50000`;
- authenticated around post 49999: 61 rows, execution **0.577 ms**; only 25–28 kB in-memory quicksorts, no spill; posts/media/votes remain index-backed;
- post-row lock: `posts_pkey`, execution **0.016 ms**;
- current-vote lookup: `post_votes_user_idx`, execution **0.005 ms**;
- upsert conflict arbiter: `post_votes_pkey`, execution **0.146 ms**;
- score update: `posts_pkey`, execution **0.119 ms**;
- no large-table sequential scans, temp spills, or unbounded row growth attributable to viewer vote state.

These numbers establish boundedness/regression acceptance only; they are not a performance-improvement claim.

### Browser evidence

Isolated API/nginx/frontend runtime backed by a disposable PostgreSQL database established:

- all six vote transitions correct in DOM and database;
- optimistic upvote visible about 250 ms after click while a deliberately delayed 1,200 ms response was still pending, followed by authoritative reconciliation;
- intentional HTTP 500 mutation failure rolled back to the last confirmed neutral score/vote and surfaced an error;
- rapid up-then-down produced two mutation requests and ended at the last intent in DOM and database without stale-response corruption;
- vote state survived select-away-and-return;
- searched-board voting preserved `q=score:>=35`;
- Back/Forward and Arrow/J/K remained coherent;
- canonical `/post/40` path/query remained unchanged by voting;
- authenticated feed and around exposed the current viewer vote;
- signed-out reads returned neutral `userVote`, signed-out mutation returned 401 without score change, and vote buttons were disabled;
- a warmed vote added **0 feed requests / 0 around requests**, **0 row mounts / 0 row unmounts**, and Long Tasks stayed **0 -> 0**;
- retained state stayed ordered/unique and within the 960-post bound.

One raw browser check initially reported `expandedScore=4041`; this is accepted as a harness regex artifact caused by concatenating `#40` and `41 points`. The same raw record had coherent score fields, and a dedicated re-probe confirmed expanded and thumbnail score equality (`40 == 40`). No application defect is indicated.

### Cleanup / gate hygiene

The local agent removed both disposable databases, stopped the temporary API/nginx runtime, removed the detached worktree and temporary API binary, and verified no `m4_%` databases remained. The canonical checkout was tracked-clean after the gate. No tracked source/config/docs, commit/ref, deployment or production-state write occurred during the acceptance gate.

Decision: **accept M4 post voting**. The SQL shapes are bounded/index-backed, mutation correctness and concurrency coverage are sufficient, optimistic/failure/rapid-action behavior is correct, and the board update scope preserves accepted M4 invariants. No additional architecture, cache layer, index or frontend store is justified by the evidence.

## Integration status

GitHub `v2` was re-verified at `a1043990f4deafb4ba19a84877fdd61fe015236e`; `astra/m4-post-voting` is a clean linear descendant with only the reviewed post-voting slice and documentation.

The primary GitHub `update_ref` connector rejected attempts to move the top-level `v2` ref during argument binding even though `refs/heads/v2` exists and the target is a verified descendant. Do **not** substitute a merge commit, squash/rebase, or reconstructed file-by-file history merely to bypass that connector limitation. The remaining integration operation is a true fast-forward of `v2` to the accepted feature head, followed by the normal post-fast-forward `v2 CI` verification and a final state-only record of that run.

The local agent's reported canonical local `v2` at `2b15aeef5662844bfc39fb62d05b5c53d49295a4` is a local-only divergence and is not integration authority; GitHub `v2` remains authoritative.

## Retained architecture / invariants

- nginx for TLS/static frontend/media/reverse proxy;
- SolidJS + TypeScript + Vite + plain CSS;
- Go + standard `net/http` + pgx/v5;
- PostgreSQL authoritative for application state and durable jobs;
- no Redis baseline dependency without measured need;
- immutable numeric relational IDs; never username as a foreign key;
- post-ID cursor pagination, never OFFSET;
- real search lexer/parser/AST and parameterized SQL only;
- selected-post UI updates immediately and never waits for network;
- bounded 960-post board retention before virtualization;
- stable row identity and targeted retained-post updates;
- hot SQL bounded/indexed for actual query shapes and checked with `EXPLAIN (ANALYZE, BUFFERS)` when changed;
- media worker concurrency remains explicitly bounded.

## Unresolved issues

- Complete the accepted post-voting fast-forward into GitHub `v2` using a mechanism that preserves the existing reviewed commit history, then verify post-fast-forward CI. This is an integration-mechanics issue only; no product/code defect remains in the post-voting slice.

## Single best next task

After the post-voting fast-forward and post-fast-forward CI succeed, implement the **M4 nested comments read/create slice** for the selected post. Add bounded/indexed comment retrieval using the existing `comments(post_id, id)` / parent indexes, authenticated comment creation with numeric user/post/parent IDs and parent-post validation, and nested SolidJS rendering without introducing comment voting yet. Keep comment voting as a later slice once the v2 comment read/create path exists and is browser-gated.
