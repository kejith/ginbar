# Ginbar v2 state / handoff

Last updated: 2026-10-07
Phase: **M5 moderation/admin/imports in progress; moderation and first imports HTTP/config slice accepted/integrated; jobs/admin observability next**
Integration branch: `v2`
Legacy branch: `master` (read-only for rewrite work)

Read this file first. Use [`PLAN.md`](PLAN.md) for stable milestone/product rules and [`PERFORMANCE.md`](PERFORMANCE.md) for accepted benchmark history. Do not use chat history as project memory.

## Current rewrite status

- M1 board performance prototype: **complete and integrated**.
- M2 fresh PostgreSQL schema + core Go API: **complete and integrated**.
- M3 media pipeline accepted scope: **complete and integrated**.
- M4 connected core product: **complete and integrated**.
  - authentication/session foundation: **accepted and integrated**;
  - connected board/API/session boundary: **accepted and integrated**;
  - search-connected board: **accepted and integrated**;
  - post voting: **accepted, SQL/browser-gated and integrated**;
  - nested comments read/create: **accepted, SQL/browser-gated and integrated**;
  - comment voting: **accepted, SQL/browser-gated and integrated**;
  - tag mutations: **accepted, SQL/API/browser-gated and integrated**;
  - public read-only profiles: **accepted, SQL/API/browser-gated and integrated**;
  - consolidated connected-core milestone gate: **accepted; M4 closed**.
- M5 moderation/admin/imports: **in progress**.
  - first post/comment moderation slice: **accepted, integrated, and post-integration CI verified green after runner remediation**;
  - first imports HTTP/config slice: **accepted, integrated, exact-candidate CI green, and live local acceptance gate passed**.

## M5 imports — first HTTP/config slice accepted and integrated

Verified implementation base:

`e9ffcf336271b66893e47f9cb21037692f4c1959`

Implementation branch:

`astra/m5-ingest-http`

Exact executable candidate:

`064a277ad17a85dc41bafe78bd28e7c7fb027e57`

Exact-candidate `v2 CI`:

- run `37542075015`, attempt 1;
- job `112537209596`;
- `head_sha=064a277ad17a85dc41bafe78bd28e7c7fb027e57`;
- workflow/job conclusion: **success**;
- exact checkout/verification: success;
- scoped correctness: **backend**;
- Go formatting check, `go vet ./...`, and `go test -v -count=1 ./...`: success with PostgreSQL-backed tests active;
- target-worker build applicability check: success;
- tracked checkout unchanged: success;
- `v2-ci: PASS sha=064a277ad17a85dc41bafe78bd28e7c7fb027e57 scope=backend`.

### Implemented application boundary

- authenticated upload route: `POST /api/v2/posts/upload?filter=sfw|nsfp|nsfw|secret`;
- upload body is multipart and accepts the file as the `file` part; the part reader is passed directly into `internal/ingest.Service`, without `ParseMultipartForm`, whole-file buffering, or a second full-file copy;
- authenticated URL-import route: `POST /api/v2/posts/import-url` with JSON `{"url":"https://...","filter":"sfw|nsfp|nsfw|secret"}`;
- both mutations use the authenticated numeric session `users.id`, require the existing same-origin policy, and return HTTP 201 with authoritative `postId` / `jobId` directly from `CreateIngestion`;
- HTTP parsing and stable API-error mapping stay in `internal/httpapi`; staging, URL fetching, SSRF/redirect validation, concurrency limiting, cleanup, and persistence semantics remain in the accepted M3 ingestion boundary;
- malformed input, invalid filters/uploads/URLs, empty sources, source-size failures, unsafe URLs, internal failures, timeouts, and ambiguous commit outcomes have explicit API behavior; ambiguous outcomes return `ingestion_outcome_unknown` and are not retried;
- production `cmd/api` constructs one `LocalStore`, one existing `HTTPURLFetcher`, and one `ingest.Service` using the same PostgreSQL store/pool as the rest of the API;
- API-wide reads retain the existing 3-second request timeout while ingestion routes use their own bounded request timeout so uploads/imports are not silently capped at 3 seconds.

### Production configuration

`GINBAR_MEDIA_SOURCE_ROOT` is required. Startup fails if it or `DATABASE_URL` is absent, or if ingestion limits are invalid/incoherent.

Defaults:

- source cap: 256 MiB;
- ingestion concurrency: 4;
- ingestion request timeout: 2 minutes;
- stage timeout: 90 seconds;
- DB timeout: 5 seconds;
- cleanup timeout: 5 seconds;
- URL connect timeout: 5 seconds;
- URL response-header timeout: 10 seconds;
- URL redirects: 5.

Operational overrides:

- `GINBAR_MEDIA_SOURCE_MAX_BYTES`;
- `GINBAR_INGEST_MAX_CONCURRENT`;
- `GINBAR_INGEST_REQUEST_TIMEOUT`;
- `GINBAR_INGEST_STAGE_TIMEOUT`;
- `GINBAR_INGEST_DB_TIMEOUT`;
- `GINBAR_INGEST_CLEANUP_TIMEOUT`;
- `GINBAR_URL_CONNECT_TIMEOUT`;
- `GINBAR_URL_RESPONSE_HEADER_TIMEOUT`;
- `GINBAR_URL_MAX_REDIRECTS`.

The URL fetch byte cap is tied to the same source-byte limit. The production HTTP server also sets a bounded request-body read timeout equal to the ingestion request timeout so a stalled multipart socket cannot hold an ingestion slot indefinitely. Its write timeout includes the ingestion request timeout, configured cleanup timeout, and response slack so definite-failure cleanup can finish before an error response is written.

### Correctness/resource evidence

Targeted HTTP/service tests cover signed-out rejection, same-origin upload and URL success, numeric-author identity, all accepted filters, invalid filters, malformed/empty/oversized uploads, missing/malformed/unsafe URLs, internal and ambiguous failures, no retry/destructive cleanup on ambiguous commit, separate ingestion request deadlines, and a 1 MiB multipart request proving the handler does not pre-buffer the full file.

The PostgreSQL-backed HTTP test uses the real `LocalStore`, real session resolution, and existing PostgreSQL ingestion repository in a disposable schema. It verifies the HTTP-returned post/job IDs against authoritative rows, numeric `author_user_id`, unreleased state, exactly one `media_sources` row, exactly one initial `media_jobs` row, a real staged source file, no feed/search/profile visibility before release, and unchanged unrelated user data.

Existing M3 tests for durable staging, source limits, definite-failure cleanup, ambiguous-commit retention, bounded concurrency, timeout propagation, URL validation/SSRF handling, and atomic PostgreSQL post/source/job creation remain unchanged and passed in the exact-candidate CI run.

No schema or SQL implementation changed. The write path still uses the existing single `CreateIngestion` transaction and returns IDs from that statement, so there is no new hot read query, no extra reconciliation round trip, no new index, and no new `EXPLAIN (ANALYZE, BUFFERS)` requirement for this slice. No Redis, cache, frontend state, polling, or release UI was added.

### Explicit acceptance pass criteria

This slice is accepted only if all of the following pass on the exact executable candidate:

1. exact detached checkout matches `064a277ad17a85dc41bafe78bd28e7c7fb027e57`, tracked files are clean, and exact-candidate CI is green;
2. authenticated same-origin multipart upload returns authoritative post/job IDs and creates one unreleased post owned by the numeric session user, exactly one source row, exactly one initial durable media job, byte-identical staged source data, and no leftover staging file;
3. unreleased ingestion posts remain absent from public feed, search, profile, media-status, and around-post surfaces while unrelated released fixture state remains unchanged;
4. controlled safe URL import succeeds without weakening SSRF protections, while loopback/private/link-local/localhost and unsupported schemes remain rejected;
5. a definite PostgreSQL persistence failure after staging removes the staged object, creates no durable post/source/job rows, and normal ingestion succeeds again after disposable fault instrumentation is removed;
6. slow and stalled request bodies are bounded by configured transport/request deadlines and leave no orphan rows or source files;
7. concurrent ingestion never exceeds configured ingestion concurrency, all admitted requests complete correctly, staging drains to empty, and process memory returns approximately to idle baseline;
8. all disposable database, filesystem, process, and test instrumentation writes are removed, with no production/shared state or tracked repository mutation by the gate;
9. no unresolved correctness, resource-bounding, authorization, SSRF, visibility, or architecture blocker remains.

### Accepted local gate

Accepted evidence package:

`m5-first-imports-20261007T085653Z.zip`

Independently verified SHA-256:

`59e0f4a694d074b7f6c53c981aaa45057d1fc3a2ef7010d4182ca801254be451`

Archive integrity passed with 98 retained entries. Raw HTTP responses, PostgreSQL state, filesystem trees, timeout/concurrency observations, CI metadata and cleanup findings were inspected.

The exact executable `064a277ad17a85dc41bafe78bd28e7c7fb027e57` passed every criterion above:

- real authenticated multipart upload returned HTTP 201 with `postId=2` / `jobId=1`; authoritative PostgreSQL state recorded numeric `author_user_id=2`, `release_state=0`, one source and one initial job; the published 16 KiB object matched the uploaded SHA-256 and `.staging` was empty;
- the processing post was absent from feed, controlled search, author profile, media-status and around-post reconstruction, while the unrelated released fixture remained unchanged;
- `https://example.com/` exercised the live URL-import success path with HTTP 201 and an unreleased URL source; loopback, RFC1918, link-local, localhost and unsupported schemes remained rejected by the candidate's SSRF policy;
- a disposable trigger-induced `media_sources` persistence failure returned HTTP 500, left post/source/job counts unchanged and left the filesystem tree byte-for-byte equivalent before/after; recovery succeeded after the trigger/function were removed;
- a trickled multipart request was cut at about 6.6 seconds under a 6-second request timeout and a fully stalled body returned bounded HTTP 504, with no row/file orphans;
- four concurrent uploads with `GINBAR_INGEST_MAX_CONCURRENT=2` produced 250 staging samples with an observed maximum of exactly two active staging files; all four returned HTTP 201, staging drained to empty, and idle RSS after the run (~13,088 kB) was approximately the pre-load baseline (~13,660 kB);
- cleanup removed all isolated API processes, disposable PostgreSQL state, media roots and temporary fault instrumentation; no prohibited production/shared write was reported.

One retained exploratory query in `12-db-after-upload.txt` referenced a nonexistent `media_sources.id` column and errored; the immediately retained corrected query in `13-media-sources.txt` established the intended source-row assertions. This is an evidence-harness query typo, not an application defect.

Decision: **accept the first M5 imports HTTP/config slice**. The existing ingestion architecture, PostgreSQL transaction shape, SSRF boundary, bounded concurrency and transport deadlines are sufficient for this slice; no new schema/index, Redis dependency, cache, frontend store, or reconciliation read is justified.

### Integration

Before integration, live `v2` was:

`e9ffcf336271b66893e47f9cb21037692f4c1959`

The implementation branch was five commits ahead and zero behind. Its documentation/state-only head was:

`268d0e2d7c0dd597ec1a7d43cf98e0ec3d8e116a`

The exact accepted executable remains:

`064a277ad17a85dc41bafe78bd28e7c7fb027e57`

GitHub compare confirms the branch head differs from that executable only by `docs/v2/STATE.md`. Remote `v2` was moved non-force with an expected-SHA lease from `e9ffcf3…` to `268d0e2…`; therefore the integrated executable files are identical to the exact accepted candidate.

## M5 post/comment moderation — accepted and integrated; exact-SHA CI green

Verified implementation base:

`b498c67f12cadb1e227433d1e529bdc57aee99a2`

Implementation branch:

`astra/m5-post-comment-moderation`

Exact locally tested executable candidate:

`baeed61e7c3684a4b973937ee56aa47f73be93c9`

Accepted evidence package:

`m5-moderation-20261006T213331Z.zip`

Independently verified SHA-256:

`ae2fc0047b560eaac38995c98be1d3f2f8b37c8f8b40815d07fb49e549edda11`

Archive integrity passed with 51 retained files. Raw API/database results, SQL plans, real-browser assertion logs/traces, CI metadata and cleanup findings were inspected.

### Accepted implementation boundary

- moderator/admin-only idempotent post and comment hide mutations;
- PostgreSQL-authoritative authorization using immutable numeric `users.id`;
- moderation audit fields preserve first-action moderation time and moderator numeric user ID while existing `deleted_at` remains the visibility/tombstone primitive;
- each moderation mutation is one atomic PostgreSQL statement that returns authoritative resulting state without a reconciliation read;
- post moderation reuses existing public feed/search/around/profile visibility predicates;
- comment moderation preserves structural tombstones, descendants and parent relationships, and rejects new replies to a moderated parent;
- frontend moderation handling remains selected-post-local, abortable and epoch/selection-fenced;
- moderated retained posts stay in board layout as disabled hidden placeholders until bounded-window replacement removes them; keyboard navigation skips them;
- restore/unmoderate, imports/jobs/admin observability, private messages and unrelated production-hardening work remain deferred.

### Accepted PostgreSQL/API/browser gate

The detached local gate tested exact SHA `baeed61e7c3684a4b973937ee56aa47f73be93c9` with a tracked-clean worktree and migrations `001` through `007`.

API/database evidence passed **38/38** checks:

- signed-out moderation returned 401;
- ordinary-member moderation returned 403;
- cross-origin moderator mutation returned 403 with zero database mutation;
- missing post/comment targets returned 404;
- moderator/admin post hides returned authoritative numeric-user audit state;
- repeated and 12-worker concurrent post moderation preserved one first-action `moderatedAt` and `moderatedByUserId`;
- moderated posts disappeared immediately from feed, controlled tag search, around-post reconstruction and author profile while unrelated rows remained correct;
- moderated comments became body-less structural tombstones, preserved their descendants under the same parent, and rejected new replies with the accepted 409 `parent_comment_deleted` contract;
- unrelated comments remained unchanged.

Measured mutation plans were bounded/index-backed:

- post hide: `posts_pkey` + `user_roles_pkey`, execution 0.357 ms, no sequential scan, sort or temp spill;
- comment hide: `comments_post_idx` / `comments_pkey` + `user_roles_pkey`, execution 0.342 ms, no sequential scan, sort or temp spill;
- both mutations remain single-statement UPDATE…RETURNING shapes with no application-level N+1 or post-mutation reconciliation read.

Real Chromium evidence passed:

- ordinary-member scenario: **8/8**;
- moderator scenario: **17/17**;
- stale-response scenario: **11/11**.

Browser evidence established authorized-control visibility, server-side ordinary-user rejection, structural comment tombstones with preserved depth, targeted post-hide state, unchanged unrelated retained rows, keyboard skipping of hidden retained posts, direct moderated-post unavailability, clean board invariants and zero observed Long Tasks in the retained member/moderator runs.

The stale-response gate held a real post-moderation request, moved selection to another post, then released it. The request ended as the application's expected `ERR_ABORTED` cancellation; selection, expanded state and route remained on the newer post. Only post-hide staleness was browser-tested because it exercises the broader route/shell/retained-thumbnail transition; comment-hide uses the same shared moderation result fence and remained unit-covered.

The retained `findings.md` states that the stale target was also confirmed unmutated in PostgreSQL, but that specific SQL check was not retained as a separate raw artifact. The route-layer abort plus retained browser evidence and the separately proven backend mutation semantics are sufficient for acceptance; this evidence-retention omission is not treated as a product defect.

Cleanup evidence established the disposable database was dropped and absent, isolated API/nginx ports were stopped, the detached worktree was removed/pruned, unrelated services remained untouched, and no prohibited tracked write was made by the local gate.

Decision: **accept the first M5 post/comment moderation slice**. No cache, new index, global store, Redis dependency, virtualization change or other architecture change is justified by the measured evidence.

### Integration

Before integration, `v2` had documentation/state-only HEAD:

`0bbf95d42f2fa72e63d40b1e2cf1545a864035bf`

Its executable parent was the original candidate base `b498c67f12cadb1e227433d1e529bdc57aee99a2`, so the tested candidate and the state commit had diverged only because the state record was created after the implementation branch.

Integration therefore used a two-parent commit rather than discarding either history:

`8bd3643060d10844769920dfffb0a7ed50c68e55`

- first parent: documentation/state HEAD `0bbf95d42f2fa72e63d40b1e2cf1545a864035bf`;
- second parent: exact tested candidate `baeed61e7c3684a4b973937ee56aa47f73be93c9`;
- the integration tree is the exact candidate tree plus the already-recorded `docs/v2/STATE.md`;
- GitHub compare confirms `8bd3643…` differs from the tested candidate only by `docs/v2/STATE.md`;
- remote `v2` moved non-force with an expected-SHA lease from `0bbf95d…` to `8bd3643…`.

The integrated executable files are therefore identical to the accepted local-gate candidate.

### Post-integration CI verification — runner remediated and exact-SHA CI green

Post-integration `v2 CI` run:

`37535985807`

Exact integrated executable SHA:

`8bd3643060d10844769920dfffb0a7ed50c68e55`

Accepted runner diagnostic evidence:

`ci-runner-diag-20261006T215043Z.zip`

SHA-256:

`a37abb0fb419795fd8e051f854d70dbf8423149e4be9de50c140ea71612f42bd`

Accepted runner remediation evidence:

`ci-runner-remediate-20261006T220241Z.zip`

Independently verified SHA-256:

`228ac0c72f7638464fa36ef28e4a241d9eb29e435a8da055d5887d8762504b0b`

Archive integrity passed. Retained service/process evidence, exact CI metadata/logs, annotation output, cleanup proof, and repository-cleanliness evidence were inspected.

The proven root cause was two live `Runner.Listener` processes sharing the single `ginbar-ci-vm` runner registration and the same runner-owned `_work`, `_temp`, and `_diag` paths. The stale listener survived service restarts because `gha-runner.service` used `KillMode=process`.

Authorized remediation completed successfully:

- stopped `gha-runner`;
- terminated the stale `run-helper.sh` / `Runner.Listener` stack after confirming service stop alone left the leaked children alive;
- changed only `gha-runner.service` from `KillMode=process` to `KillMode=control-group`;
- daemon-reloaded and started the service;
- verified one listener for runner registration `ginbar-ci-vm`, id 3;
- performed one controlled service restart and proved the old listener was reaped and exactly one fresh listener remained in the service cgroup;
- left the corrected service running with one listener.

Post-remediation exact-SHA CI:

- run `37535985807`, attempt 4;
- job `112525700832`;
- `head_sha=8bd3643060d10844769920dfffb0a7ed50c68e55`;
- workflow/job conclusion: **success**;
- checkout exact revision: success;
- exact SHA verification: success;
- scoped correctness gate: success;
- `v2-ci: PASS sha=8bd3643060d10844769920dfffb0a7ed50c68e55 scope=all`;
- target-worker build applicability check: success;
- tracked checkout unchanged: success;
- one `Set up job` step, zero annotations, no `_diag/pages/... already exists` and no `_runner_file_commands/set_output_*` failures.

The available local-agent token lacked `actions:write`, so its rerun API attempts returned 403 and the user triggered attempt 4 through the GitHub UI. This affects only how the rerun was initiated, not the validity of the exact-SHA CI result.

Decision: **runner blocker resolved; post-integration moderation CI accepted green**. No Ginbar repository/workflow workaround is required.

## M4 connected-core milestone gate — accepted; M4 complete

Exact executable tested:

`6fb51b6c11891f7ab47d071d8964ed1bd83f94d2`

Gate-time documentation/state head:

`38372bc3976cf26de4b1fefb39b84c05ad0d9924`

Applicable post-integration `v2 CI` run `37472400920`, job `112298986709`: **success** on the exact executable SHA.

Evidence package:

`m4-consolidated-20261006T155814Z.zip`

Independently verified SHA-256:

`deabd4abede53b30e9d4205e8cb5454c30c3563753243dbd1c5e4ec577a6dd98`

Archive integrity passed with 40 retained files. Raw API/database evidence, committed EXPLAIN output, real-browser assertions/traces, stale-response ordering, CI metadata and cleanup findings were inspected.

### Consolidated gate facts

- Isolated PostgreSQL 16.15 applied migrations `001` through `006` and used the committed 100,000-post / 1,000-user / 100,000-media / 300,000-post-tag fixture plus committed vote/comment seeds and gate-specific disposable rows.
- Invitation-only registration, login, immutable numeric session identity, session continuity and logout invalidation were exercised. Signed-out auth and mutation boundaries returned the expected 401 responses.
- Public feed IDs were descending and unique; direct around-post reconstruction and included-tag, excluded-tag and numeric-predicate search all succeeded.
- Authenticated post voting, nested comment read/create, comment voting, tag add, ordinary-user tag-removal rejection, moderator tag removal and repeated idempotent removal all matched authoritative PostgreSQL state.
- Public profiles returned only the accepted `id` / `username` / `createdAt` metadata and two 33-post cursor pages with descending unique IDs and zero overlap.
- The primary real-Chromium scenario preserved board selection placement, same-row/cross-row navigation, canonical post routes, Back/Forward, Arrow/J/K navigation, search canonicality, mutation update scope, profile navigation and return-to-board invariants.
- Initial browser checks for nested-comment proof, comment score text and profile ordering contained gate-script selector/type mistakes. Follow-up evidence established nested depth-1 replies and reply creation, authoritative comment score/vote `1` with the intended upvote pressed and tree intact, and integer-descending profile IDs. These were harness defects, not application defects.
- A delayed real tag response for post 99996 was released after selection moved to 99992; selection remained 99992, the tag panel remained fenced to 99992 and application invariants stayed clean. The observed `ERR_ABORTED` was the intended cancellation.
- Main/follow-up/moderator browser runs recorded zero failed requests; representative real AVIF media was served by the isolated nginx fixture. The stale test's console 401 was the expected signed-out `/auth/me` probe.
- `PerformanceObserver(type=longtask)` observed **0 Long Tasks** across representative warmed board, mutation, comment/tag and profile interactions. Final retained board state was **120 posts**, below the established **960-post** bound, with `assertInvariants()` clean.
- Candidate hot SQL remained bounded/index-backed. The only sequential scans were the 100-row `tags` dimension, sorts were bounded in-memory quicksorts, and no temp spill was observed. Current feed/search/vote/comment/profile/auth shapes in the committed fixtures remained sub-millisecond on this gate host.
- `explain_compare.sql` also intentionally retains a historical comparison shape whose older-cursor media merge scanned about 50,077 media rows and took **4.507 ms**; its bounded counterpart in the same fixture took **0.059 ms**. This comparison baseline is not the active candidate query shape and is retained here to avoid mischaracterizing every plan in the archive as sub-millisecond.
- No application-level N+1 pattern, large-relation sequential scan, unbounded sort, pool/transaction deviation or concrete cross-slice regression was found.
- The M4 requirement review matched `PLAN.md`: auth/invite registration, connected board, filters/search, votes, tags, nested comments and profiles were all exercised. **No concrete unimplemented M4 requirement was found.**
- Cleanup findings record the disposable database dropped, temporary API/nginx processes stopped, detached gate worktree removed/pruned, unrelated services untouched and canonical tracked status clean.

Decision: **accept the consolidated M4 connected-core milestone gate and close M4**. The integrated product preserves the accepted correctness, authorization, navigation, bounded-state, stale-response and representative browser-performance invariants. No cache, index, schema, global-store, virtualization or other architecture change is justified by this gate.

## M4 profiles — accepted and integrated

Verified live GitHub `v2` base before integration:

`ae42dc67afc4885196e9580dad79ec83ea66befd`

Implementation branch:

`astra/m4-profiles`

Exact executable candidate:

`6fb51b6c11891f7ab47d071d8964ed1bd83f94d2`

Exact-candidate `v2 CI` run `37429479017`, job `112156704423`: **success**.

- exact SHA checkout/verification passed;
- scoped v2 correctness gate passed;
- target-worker release-build verification passed;
- tracked checkout remained unchanged.

### Accepted implementation boundary

- Canonical public profile route is `/user/:id` and uses immutable numeric `users.id`.
- Public API is `GET /api/v2/users/:id`.
- Public user metadata is limited to numeric `id`, current `username`, and `createdAt`.
- Profile posts use post-ID descending cursor pagination with default page size 33 and maximum 120; no OFFSET pagination.
- Public profile posts expose only rows authored by that user that are released, nondeleted, SFW, and have ready media.
- Profile SQL reuses the existing signed-out post projection/ready-media boundary; no role, credential, invitation, moderation, messaging, comment-history, profile-edit, bio, avatar, display-name, or username-rename surface was added.
- Frontend profile state is page-local. Requests are abortable and sequence-fenced; delayed responses cannot update an unmounted/newer document state.
- `Load more` merges by immutable post ID, keeps descending ordering, and avoids duplicates.
- Clicking a profile thumbnail uses canonical `/post/:id`; browser Back reconstructs the profile coherently.
- No schema migration, Redis dependency, cache, new global store, virtualization, event stream, or speculative index was added.

### Accepted PostgreSQL/API/browser gate

Evidence package:

`m4-profile-20261006T131014Z.zip`

Independently verified SHA-256:

`eb0448412ad3bdca1eb3738dbe8805885ab7cb163ee138c01ff4b701b9bdf146`

Archive integrity passed. Raw PostgreSQL plans, API request/response/state evidence, browser assertions/request ordering, Playwright traces, CI metadata, long-task observations, and cleanup evidence were inspected.

The disposable PostgreSQL 16.15 fixture applied migrations `001` through `006` and contained 1,000 users, 100,000 posts, 100,000 media rows and the committed benchmark tag relations before gate-specific profile rows.

`EXPLAIN (ANALYZE, BUFFERS)` evidence:

- active user lookup used `users_pkey`, returned one row, no rows removed, shared-hit buffers 6, planning 0.173 ms, execution 0.038 ms;
- committed seed-only first profile page used `posts_author_idx` plus `media_pkey`, returned zero eligible rows after filtering 100 user rows, no sort/temp spill, planning 0.219 ms, execution 0.195 ms;
- committed seed-only older-cursor page used the same indexes with `id < 50000`, filtered 50 rows, no sort/temp spill, planning 0.081 ms, execution 0.037 ms;
- supplemental populated first page returned 34 rows through `posts_author_idx` + `media_pkey`, shared-hit buffers 109, no sort/temp spill, planning 0.287 ms, execution 0.082 ms;
- supplemental populated older-cursor shape returned 7 rows after scanning/filtering 104 ineligible author rows and one non-ready media row, shared-hit buffers 131, no sort/temp spill, planning 0.093 ms, execution 0.226 ms;
- supplemental seeded-SFW user shape returned 34 rows, shared-hit buffers 139, no sort/temp spill, planning 0.055 ms, execution 0.174 ms.

There was no sequential scan over a large relation, no explicit sort node, no temp-file spill, and no application-level N+1 pattern. The profile request performs a fixed public-user lookup plus one bounded/index-backed post-page query. The older-cursor populated plan demonstrates predicate filtering can inspect more author rows than it returns, but measured work remained small at the 100,000-post fixture and does not justify a new partial index without evidence from a materially worse real query distribution.

Live API evidence on the same fixture established:

- signed-out `GET /api/v2/users/500?limit=33` returned 200 with exactly `[createdAt, id, username]` user metadata;
- page 1 contained 33 descending unique post IDs and `nextBefore=100094`;
- the next cursor page contained 7 descending unique IDs, zero overlap, preserved cross-page descending order, and exhausted the 40 eligible gate rows;
- controlled NSFW/non-SFW, unreleased, deleted, not-ready, and other-author posts were all excluded;
- `/api/v2/users/1001` returned the intended 404 `user_not_found` response.

Real-browser evidence:

- primary profile scenario: **17/17 assertions passed**;
- stale-response scenario: **8/8 assertions passed**;
- direct `/user/500` rendered `bench-user-500`, numeric ID and member-since metadata;
- initial grid contained exactly 33 descending unique posts;
- `Load more` produced 40 descending unique posts without duplicates/reordering and hid when exhausted;
- thumbnail navigation reached canonical `/post/100126`; Back returned coherently to `/user/500`;
- `/user/1001` rendered the intended not-found state;
- delayed real `/api/v2/users/500` delivery after leaving the profile could not change the newer `/` document or leak user-500 state into a subsequent `/user/501` document;
- `PerformanceObserver(type=longtask)` recorded 0 long tasks for both the initial profile render and warmed pagination interaction;
- browser runs recorded 0 page errors and 0 transport-level failed requests.

The console contained HTTP error messages only for the isolated fixture's intentionally absent media bytes (`*.thumb.avif`/full media paths) plus the existing expected signed-out `/api/v2/auth/me` 401 on board navigation. These were cross-checked against request/nginx evidence and are test-harness/existing-auth artifacts, not profile-code errors. No acceptance decision is based on the missing fixture media bytes.

Cleanup evidence established that the temporary API/nginx processes were terminated, disposable database `m4prof_20261006t131014z` was dropped and verified absent, the detached gate worktree was removed/pruned, and the canonical tracked checkout remained clean. No prohibited production/shared persistent-state write occurred.

Decision: **accept M4 public read-only profiles**. The slice satisfies the intended numeric-identity/public-metadata boundary, bounded cursor paging, visibility filters, canonical navigation, stale-response isolation, and measured SQL/browser behavior. Existing indexes are sufficient for the measured query shapes. No new index, cache, schema change, global profile store, polling layer, or virtualization change is justified by this evidence.

### Integration verification

The accepted executable was non-force fast-forwarded on remote `v2`:

`ae42dc67afc4885196e9580dad79ec83ea66befd -> 6fb51b6c11891f7ab47d071d8964ed1bd83f94d2`

The candidate was exactly three commits ahead of the verified base with no divergence. No merge commit or force update was used.

Post-fast-forward `v2 CI` run `37472400920`, job `112298986709`: **success** on exact integrated executable `6fb51b6c11891f7ab47d071d8964ed1bd83f94d2`.

- `head_branch=v2` and `head_sha=6fb51b6c11891f7ab47d071d8964ed1bd83f94d2`;
- exact checkout and SHA verification succeeded;
- scoped v2 correctness gate succeeded;
- target-worker release-build verification succeeded;
- tracked checkout remained unchanged;
- all workflow steps completed successfully.

Integration decision: **profiles are accepted and integrated; the profile slice is closed**.

## Other accepted M4 slices

### Tag mutations

Exact executable `1d67cfde2847a3b10aa44bb799ae878a39e36a55`; exact-candidate CI run `37359169575`, job `111929122699`, success. Accepted evidence `m4-tag-20261005T185611Z.zip`, SHA-256 `a9ed36119c982a73bd2de0bfd756035dde027bc7d298ebbe7c281d0f42f13e5d`, plus browser supplement `m4-tag-browser-supplement-20261006T072000Z.zip`, SHA-256 `238a863eedceb9bd0fdff161d985fe2b6ef6073c3a39e3c33b139999169a27ae`. PostgreSQL-authoritative add/remove, moderator/admin removal authorization, concurrency/idempotence, immediate search semantics, ambiguous-failure reconciliation and stale-selection fencing were accepted. Post-integration `v2 CI` run `37423060375`, job `112136510174`, succeeded.

### Comment voting

Exact executable `005b10ffa4b6f344d64ae6b0e9b5246a15527244`; exact-candidate CI run `37342739594`, job `111873754474`, success. Accepted evidence `m4-comment-voting-20261005T170504Z.zip`, SHA-256 `481b990f4c3d4ac6a8f1e7b4b504733b7b80fef8d5e199e4d1ab74d2e234e356`. PostgreSQL-authoritative explicit `-1/0/+1` voting, bounded viewer-vote reads, row-locked score deltas, per-comment frontend sequencing, optimistic rollback and stale-selection isolation were accepted. Post-integration `v2 CI` run `37351495807`, job `111903167789`, succeeded.

### Nested comments read/create

Exact executable `d83ffa69c6a80418f13c5e4a2b5af19004846a51`; exact-candidate CI run `37334651076`, job `111846241798`, success. Accepted evidence `m4-comments-20261005T000000Z.zip`, SHA-256 `8c1ca3f71753e449db451c6ed33412bf9451bc4e2345f92b62a64b18e764e7cf`. Bounded ascending comment-ID reads, structural tombstones, authenticated same-origin creation, local selected-post state, abort/epoch guards and iterative tree construction were accepted. Post-integration `v2 CI` run `37339584913`, job `111862933910`, succeeded.

### Post voting

Exact executable `e1c5d1f65e72a81615164bc6445bcdc2d8218381`; exact-candidate CI run `37313953385`, success. Accepted evidence `m4-post-voting-20261005T133638Z.zip`, SHA-256 `4cf5c5839e7cfc427465821600bcf08a3bb56e9b5a22dbdead4fd185ca396b14`. PostgreSQL-authoritative explicit `-1/0/+1` state, post-row serialization, bounded viewer-vote reads and targeted optimistic frontend mutation were accepted. Post-integration `v2 CI` run `37321333276` succeeded.

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

Do not pull these into the next task without a concrete requirement:

- broader video transcoding or exact WebM/EBML acceptance;
- media orphan/janitor hardening;
- deployment UID/GID/media-storage permissions;
- stronger filesystem hardening if the media-tree threat model changes;
- auth abuse/rate limits and KDF admission controls before production-hardening evidence requires them;
- virtualization, Redis synchronization, event streams or a large frontend store;
- v1-v2 end-to-end speedup claims before an apples-to-apples benchmark exists;
- profile edit/bio/avatar/display-name/rename functionality until a later explicit product slice requires it.

## Unresolved issues

No unresolved correctness, SQL-plan, browser-performance, integration or architecture blocker remains from M4 after the accepted consolidated milestone gate.

The profile older-cursor plan can inspect filtered author rows before finding an eligible SFW/ready row. Current 100,000-post evidence remains small and index-backed; do not add a partial profile index without a real distribution/latency signal that justifies it.

The profile browser gate's media 404 console messages came from benchmark storage keys without corresponding media files in the disposable nginx tree. This did not affect profile route/API/state assertions and is not a candidate defect, but future consolidated browser gates should use real served fixture media when practical so console-noise checks are cleaner.

## Single best next task

Implement the first M5 jobs/admin observability slice from the current live `v2`: add a minimal moderator/admin-only read API for durable media-job inspection that exposes bounded, cursor-paginated operational state needed to diagnose queued/running/retry/failed work without adding job mutation/retry controls yet. Keep PostgreSQL authoritative, use immutable numeric authorization identity, avoid OFFSET/N+1/reconciliation reads, make the query shape bounded/index-backed and verify it with `EXPLAIN (ANALYZE, BUFFERS)`, add targeted API/PostgreSQL authorization and pagination tests, require exact-candidate CI green, and do not add Redis, polling infrastructure, dashboards, bulk import, job cancellation/retry mutation, or unrelated M5 work.
