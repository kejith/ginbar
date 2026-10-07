# Ginbar v2 state / handoff

Last updated: 2026-10-07
Phase: **M5 moderation/admin/imports in progress; first regeneration/admin mutation accepted, integrated, and post-integration CI green; consolidated M5 gate next**
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
  - first imports HTTP/config slice: **accepted, integrated, exact-candidate CI green, and live local acceptance gate passed**;
  - first jobs/admin observability slice: **accepted, integrated, exact-candidate/local-gate/post-integration CI green**;
  - first role-administration slice: **accepted, integrated, exact-candidate/post-integration CI green**.
  - second role-administration/bootstrap slice: **accepted, integrated, exact-candidate/post-integration CI green**.
  - first regeneration/admin mutation slice: **accepted, integrated, exact-candidate/post-integration CI green**.

## M5 regeneration/admin mutation — accepted and integrated

Verified implementation base:

`ff7468bd16e754216c21bc070123bdff368e28be`

Implementation branch:

`astra/m5-regeneration-admin`

Exact accepted executable candidate:

`640311fc216435be90588484cbbc0624a097f598`

Exact-candidate `v2 CI`:

- run `37618638054`, attempt 1;
- job `112783313294`;
- `head_sha=640311fc216435be90588484cbbc0624a097f598`;
- workflow/job conclusion: **success**;
- exact checkout/SHA verification, Go formatting, `go vet ./...`, PostgreSQL-backed `go test -v -count=1 ./...`, target-worker build applicability, and tracked-clean verification all passed;
- `v2-ci: PASS sha=640311fc216435be90588484cbbc0624a097f598 scope=backend`.

Earlier branch heads were not accepted. `bd112810358d2ff47f41005007ee1ef96b135b07` and `b5133f991d68cf221a16c4fc332ea2dff7c2e1ff` exposed gofmt-only defects. `392c95cc7dcdce6d8ef5f30e961841d3ca2137db` reached the PostgreSQL suite and exposed only test-fixture/plan-assertion defects: an invalid synthetic running-job fixture, overlong fixture usernames, and an assertion expecting `posts_pkey` where PostgreSQL correctly selected the released-post partial index. All were corrected before the accepted candidate.

### Accepted boundary and HTTP contract

- narrow mutation: `POST /api/v2/admin/posts/{id}/regeneration`, where `{id}` is immutable numeric post identity;
- the existing authenticated-session boundary still provides signed-out 401 `unauthenticated`, and the existing same-origin mutation policy rejects cross-origin requests before durable work;
- actor authorization uses immutable numeric `users.id` and the authoritative PostgreSQL `user_roles` row for `role.Admin`; ordinary members and moderators receive 403 `forbidden`;
- admin authorization and regeneration selection/mutation occur in the same bounded PostgreSQL statement, avoiding a race-prone preflight role read and avoiding an application-side reconciliation query;
- the HTTP/service layer delegates state transitions to the existing regeneration workflow instead of duplicating media-job state logic;
- success returns HTTP 202 with authoritative `postId`, immutable `jobId`, and string outcome `queued`, `coalesced`, or `superseded` directly from the mutation;
- invalid numeric identity retains 400 `invalid_post_id`; missing or otherwise non-regenerable released/ready targets return stable 409 `post_not_regenerable`; internal failures retain the existing 500 `internal` contract;
- pending work coalesces without changing attempts, availability, last error, or lease generation;
- running work is superseded to pending, claim/lease state is cleared, retry state resets, and lease generation increments so the old worker can no longer complete with its stale ownership token;
- succeeded and failed work requeue with the existing reset semantics;
- released ready media rows and their storage identity remain unchanged while regeneration is merely pending/running;
- repeated/concurrent requests serialize on the selected durable job and produce one queued result followed by coalesced results without duplicate active work;
- no bulk regeneration, generic retry/cancel control, polling/event stream, frontend admin dashboard, Redis/cache state, schema migration, or speculative index was added.

### Correctness and SQL-plan evidence

Exact-candidate PostgreSQL/HTTP tests passed for:

- signed-out, member, moderator and cross-origin rejection, including no durable mutation for rejected authenticated requests;
- real authenticated admin HTTP success for succeeded, failed, pending and running durable-job states;
- invalid post identity, missing/non-regenerable target and stable HTTP/error mapping;
- pending coalescing with attempts, availability, last error and generation preserved;
- running supersession with generation increment and explicit proof that the old worker's prior `claimed_by`/generation lease token can no longer complete the job;
- succeeded/failed reset/requeue semantics;
- preservation of currently published ready media;
- 12 concurrent admin regeneration requests yielding exactly one queued outcome, 11 coalesced outcomes, one immutable job ID and one active durable job;
- existing regeneration, worker/media-job, ingestion, moderation, role-administration, tag and jobs-observability behavior through the full backend suite.

On a meaningful fixture with approximately 5,000 role rows and 5,000 released/ready post-source-media-job rows, retained `EXPLAIN (ANALYZE, BUFFERS)` evidence for the exact authorized mutation showed:

- admin authorization through `user_roles_pkey`;
- post/kind job lookup through `media_jobs_post_kind_id_idx`;
- released/nondeleted post lookup through `posts_feed_released_idx`;
- source and current-media lookups through `media_sources_pkey` and `media_pkey`;
- the requeue update targets the immutable job through `media_jobs_pkey`;
- the bounded target preference sort was an in-memory quicksort using 25 kB;
- planning time was **2.435 ms** and execution time **0.569 ms** with shared-buffer hits only;
- no large-relation sequential scan, external merge, temp spill, OFFSET, application-side filtering, N+1 pattern, preflight authorization read, or post-mutation reconciliation read was introduced.

Decision: **accept the first M5 regeneration/admin mutation slice**. Exact-candidate CI exercises the full PostgreSQL authorization, concurrency, fencing and HTTP behavior relevant to this backend-only slice; no browser or target-host local-agent gate is required before integration. Existing indexes are sufficient and no new index is justified.

### Integration and post-integration CI

Immediately before integration, live `v2` was:

`ff7468bd16e754216c21bc070123bdff368e28be`

The accepted implementation/state branch head was 19 commits ahead and zero behind:

`43b8fea82bb10202170dce20c5b396759bf7f05b`

Remote `v2` was fast-forwarded non-force with expected-SHA lease from `ff7468bd16e754216c21bc070123bdff368e28be` to `43b8fea82bb10202170dce20c5b396759bf7f05b`. The only commit above exact accepted executable `640311fc216435be90588484cbbc0624a097f598` is this slice's documentation/state acceptance commit, so integrated application files are identical to the accepted candidate.

Post-integration `v2 CI`:

- run `37619197254`;
- job `112784890365`;
- `head_branch=v2`;
- exact integrated head `43b8fea82bb10202170dce20c5b396759bf7f05b`;
- conclusion: **success**;
- exact checkout/SHA verification, scoped backend correctness, target-worker build applicability, and tracked-clean verification all passed;
- `v2-ci: PASS sha=43b8fea82bb10202170dce20c5b396759bf7f05b scope=backend`.

Integration decision: **the first M5 regeneration/admin mutation slice is closed**. Exact executable application state is `640311fc216435be90588484cbbc0624a097f598`; state/documentation commits above it do not change application files.

## M5 role administration — first-admin bootstrap and admin-role mutation accepted and integrated

Verified implementation base:

`5e2f108fe43fc473349b52337e513c1a816cf52b`

Implementation branch:

`astra/m5-admin-bootstrap`

Exact accepted executable candidate:

`641d2985eefec3325455d9759937a8f85e3df89f`

Exact-candidate `v2 CI`:

- run `37614891041`, attempt 1;
- job `112770739288`;
- `head_sha=641d2985eefec3325455d9759937a8f85e3df89f`;
- workflow/job conclusion: **success**;
- exact checkout/SHA verification, Go formatting, `go vet ./...`, PostgreSQL-backed `go test -v -count=1 ./...`, target-worker build applicability, and tracked-clean verification all passed;
- `v2-ci: PASS sha=641d2985eefec3325455d9759937a8f85e3df89f scope=backend`.

Earlier branch heads were not accepted: `4aa22fc4edd908fba71cbe59ab5ba37849d5577d` exposed an obsolete first-slice route-absence assertion plus an over-constrained tiny-fixture plan assertion, and `96e1b6ab239b8c9fdf82a3262bf0430303869230` exposed one gofmt issue. Both were corrected before the accepted exact candidate.

### Accepted boundary and policy

- first-admin bootstrap is an explicit local operational command, `go run ./cmd/bootstrap-admin --user-id <numeric-user-id>`, using `DATABASE_URL` directly; no unauthenticated HTTP bootstrap endpoint exists;
- bootstrap accepts only an existing immutable numeric `users.id`, is one-shot once any admin exists, and records the bootstrapped numeric user as `granted_by_user_id` bootstrap provenance;
- after bootstrap, admin inspection remains `GET /api/v2/admin/users/{id}/roles`, while admin grant/revoke is exposed only to an authenticated existing admin through same-origin `PUT` / `DELETE /api/v2/admin/users/{id}/roles/admin`;
- signed-out HTTP access retains 401 `unauthenticated`; ordinary members and moderators retain 403 `forbidden`; missing targets retain stable 404 `user_not_found`;
- admin grant is idempotent on the existing `(user_id, role)` primary key and preserves the original numeric grantor on repeated/concurrent grants;
- admin revoke is idempotent for an absent target role;
- explicit self-revocation policy is **forbidden for all admins**, returning 409 `self_admin_revoke_forbidden`; this is the smallest policy that guarantees a successful revoke leaves the acting admin in place;
- admin revocation acquires a transaction-scoped PostgreSQL advisory lock in a separate statement and then revalidates actor admin authority in the mutation statement under a fresh READ COMMITTED snapshot; concurrent cross-revocations therefore cannot remove the final admin;
- bootstrap uses the same advisory-lock domain so concurrent first-admin attempts create exactly one initial admin;
- grant/revoke/bootstrap return authoritative resulting role state from their bounded PostgreSQL operation without an application-side reconciliation read;
- existing moderator-role administration SQL/routes remain unchanged; existing moderation, tag-removal, ingestion, media-job observability and worker authorization/state semantics remain PostgreSQL-authoritative and unchanged;
- no schema migration, role index, Redis/cache/event stream, frontend admin dashboard, bulk user listing, invitation administration, account disable/delete, or username-based authorization identity was added.

### Correctness and SQL-plan evidence

Exact-candidate PostgreSQL/HTTP tests passed for:

- first-admin bootstrap, missing-target rejection, one-shot behavior, numeric bootstrap provenance and concurrent bootstrap attempts;
- admin role read/grant/repeated grant/revoke/repeated revoke;
- signed-out/member/moderator rejection, same-origin mutation behavior, stable HTTP/error contracts and invalid numeric identity;
- explicit self-revocation rejection and single-admin lockout protection;
- concurrent cross-admin revocation, where exactly one revoke succeeds and the other loses admin authority before acting, leaving exactly one admin;
- 12 concurrent admin grants, yielding one role row with one preserved original grantor;
- immediate authorization propagation: a newly granted admin immediately passes the existing media-job administration PostgreSQL check and immediately loses that authority after revoke;
- preservation of the accepted moderator-role administration tests and the broader backend suite.

Retained `EXPLAIN (ANALYZE, BUFFERS)` evidence showed:

- fresh-install bootstrap with 5,001 users used `users_pkey` for the target and `user_roles_pkey` for conflict arbitration; execution was **0.864 ms** including FK triggers;
- bootstrap's role-only existence/moderator probes sequentially scanned the intentionally empty fresh-install `user_roles` relation, taking about **0.002 ms**; this one-shot empty-relation shape does not justify a speculative role index;
- admin grant on a 5,000-role fixture used `user_roles_pkey` for actor/role access, `users_pkey` for the target and `user_roles_pkey` as the conflict arbiter; execution was **1.501 ms** including FK triggers;
- admin revoke on the same meaningful fixture used `user_roles_pkey` for actor, target-role delete and moderator-state access plus `users_pkey` for the target; execution was **0.593 ms**;
- no large-relation sequential scan, unbounded sort, temp spill, OFFSET or application-side filtering/reconciliation was introduced.

Decision: **accept the second M5 role-administration slice**. The exact-candidate CI exercises the real PostgreSQL concurrency and SQL-plan boundary for this backend-only slice; no browser or target-host local-agent gate is required before integration. No new role index is justified by the retained plans.

### Integration and post-integration CI

Immediately before integration, live `v2` was:

`5e2f108fe43fc473349b52337e513c1a816cf52b`

The implementation/state branch head was 18 commits ahead and zero behind:

`ff634aeb29328d30693a2c03559c3d74dd535fe5`

Remote `v2` was fast-forwarded non-force with expected-SHA lease from `5e2f108fe43fc473349b52337e513c1a816cf52b` to `ff634aeb29328d30693a2c03559c3d74dd535fe5`. The only commit above exact accepted executable `641d2985eefec3325455d9759937a8f85e3df89f` is this slice's documentation/state acceptance commit, so integrated application files are identical to the accepted candidate.

Post-integration `v2 CI`:

- run `37615393543`;
- job `112772383796`;
- `head_branch=v2`;
- exact integrated head `ff634aeb29328d30693a2c03559c3d74dd535fe5`;
- conclusion: **success**;
- exact checkout/SHA verification, scoped correctness, target-worker build applicability, and tracked-clean verification all passed.

Integration decision: **the second M5 role-administration slice is closed**. Exact executable application state is `641d2985eefec3325455d9759937a8f85e3df89f`; this final state update is documentation-only.

## M5 role administration — accepted and integrated

Verified implementation base:

`9d562f46153510d39ff21e7bef25908ef69ebe22`

Implementation branch:

`astra/m5-role-admin`

Exact accepted executable candidate:

`2aaf24301a1d0129e7827e1069550e9da722f0ba`

Exact-candidate `v2 CI`:

- run `37611366103`, attempt 1;
- job `112759121707`;
- `head_sha=2aaf24301a1d0129e7827e1069550e9da722f0ba`;
- workflow/job conclusion: **success**;
- backend scope passed the Go 1.25 formatting gate, `go vet ./...`, PostgreSQL-backed `go test -v -count=1 ./...`, target-worker build applicability, and tracked-clean verification;
- `v2-ci: PASS sha=2aaf24301a1d0129e7827e1069550e9da722f0ba scope=backend`.

An earlier exact branch head `dd113c7b5011f6bf21cdb7247a664296d28c2897` failed only because `internal/postgres/role_admin.go` was not gofmt-clean. The formatting defect was corrected before the accepted candidate.

### Accepted boundary

- admin-only elevated-role read: `GET /api/v2/admin/users/{id}/roles`;
- explicit moderator grant/revoke only: `PUT` / `DELETE /api/v2/admin/users/{id}/roles/moderator`;
- target and actor identity are immutable numeric `users.id`; no username authorization identity was added;
- signed-out callers use the established 401 `unauthenticated` contract; authenticated members and moderators receive 403 `forbidden`;
- only an existing PostgreSQL `role.Admin` row authorizes reads or mutations;
- missing targets return stable 404 `user_not_found` and cannot create orphan role state;
- grant uses the existing `(user_id, role)` primary key and records the numeric granting admin in `granted_by_user_id`;
- repeated/concurrent grants use the primary-key conflict path while preserving the original grantor instead of rewriting audit ownership; repeated/concurrent revokes remain idempotent;
- each read or mutation is one bounded parameterized PostgreSQL statement and returns authoritative resulting moderator/admin state directly, with no application-side reconciliation read;
- existing moderation/tag authorization continues to read `user_roles` directly, so role changes require no cache refresh or duplicated authorization model;
- no admin-role mutation route exists, and no bootstrap/last-admin/self-lockout policy was introduced;
- no schema migration, new index, Redis/cache/event stream, bulk user listing, frontend admin dashboard, invitation administration, or account disable/delete surface was added.

### Correctness and SQL-plan evidence

The real PostgreSQL/HTTP tests passed:

- admin role-state inspection, moderator grant/repeated grant/revoke/repeated revoke;
- 401 signed-out and 403 member/moderator rejection;
- numeric target/grantor identity, stable missing-target behavior, same-origin mutation enforcement, and absence of any admin-role mutation route;
- original-grantor preservation across a later admin's repeated grant;
- 12 concurrent grants produced exactly one moderator row; 12 concurrent revokes left zero moderator rows;
- a newly granted moderator immediately succeeded through the existing `HidePost` authorization path, and the same user was immediately rejected after revocation;
- existing tag-removal authorization tests remained green.

On a 5,000-user/role fixture with `ANALYZE users, user_roles`, retained `EXPLAIN (ANALYZE, BUFFERS)` evidence showed:

- role read: `users_pkey` plus `user_roles_pkey`, execution 0.209 ms, shared-buffer hits only;
- moderator grant: admin/target/admin-state access through those primary keys and conflict arbitration by `user_roles_pkey`, execution 0.547 ms including FK triggers;
- moderator revoke: primary-key actor/target/delete/admin-state access, execution 0.278 ms;
- no sequential scan on `users` or `user_roles`, external merge, temp spill, OFFSET, or speculative index requirement.

Decision: **accept the first M5 role-administration slice**. Exact-candidate CI exercises the complete server/PostgreSQL behavior relevant to this backend-only slice, so no browser or target-host local-agent gate was required before integration.

### Integration and post-integration CI

Immediately before integration, live `v2` was:

`9d562f46153510d39ff21e7bef25908ef69ebe22`

The implementation/state branch head was nine commits ahead and zero behind:

`2dc3fd68d8db1271c732dcf047b1dc97ea87adc2`

Remote `v2` was fast-forwarded non-force with expected-SHA lease from `9d562f46153510d39ff21e7bef25908ef69ebe22` to `2dc3fd68d8db1271c732dcf047b1dc97ea87adc2`. The only commit above exact accepted executable `2aaf24301a1d0129e7827e1069550e9da722f0ba` is this slice's documentation/state acceptance commit, so integrated application files are identical to the accepted candidate.

Post-integration `v2 CI`:

- run `37611691335`;
- job `112760180964`;
- `head_branch=v2`;
- exact integrated head `2dc3fd68d8db1271c732dcf047b1dc97ea87adc2`;
- conclusion: **success**;
- exact checkout/SHA verification, scoped correctness, target-worker build applicability, and tracked-clean verification all passed.

Integration decision: **the first M5 role-administration slice is closed**.

## M5 jobs/admin observability — accepted and integrated

Verified implementation base:

`def1e1a99b57daa9b32776c1a7108292e7f1bcee`

Implementation branch:

`astra/m5-media-job-observability`

Exact accepted executable:

`08ed07455bd568914f1a23f5831823defaabb88a`

Exact-candidate `v2 CI`:

- run `37604661118`, attempt 1;
- job `112737109634`;
- `head_sha=08ed07455bd568914f1a23f5831823defaabb88a`;
- workflow/job conclusion: **success**;
- exact checkout/verification, backend formatting/vet/compiler checks, PostgreSQL-backed `go test -v -count=1 ./...`, target-worker build applicability, and tracked-clean checks all passed;
- `v2-ci: PASS sha=08ed07455bd568914f1a23f5831823defaabb88a scope=backend`.

An earlier branch candidate `42641632868368c236b8f90c42bce1bf7e02f704` failed only the gofmt cleanliness check for `internal/httpapi/server.go`; that formatting defect was corrected before the accepted candidate.

### Accepted boundary

- read-only moderator/admin endpoint: `GET /api/v2/admin/media-jobs`;
- authorization is server-side and PostgreSQL-authoritative from immutable numeric session `users.id` through existing `user_roles`;
- signed-out access uses the established 401 `unauthenticated` contract; ordinary authenticated members receive 403 `forbidden`;
- pages are ordered by immutable `media_jobs.id DESC`, use optional `before=<job-id>` cursor pagination, never OFFSET, default to 50 rows and cap at 100;
- the request performs one parameterized PostgreSQL statement combining role authorization and the bounded job read, with no N+1 or post-query reconciliation read;
- the first slice intentionally has no state filter and adds no index; queued versus retry work is distinguishable from existing state/attempt/availability metadata;
- output is limited to job/post identity, kind/state/priority, attempts, availability, claim/lease/generation metadata, bounded error diagnostics, and timestamps;
- `claimedBy` is projected to at most 256 characters and `lastError` to at most 2048 characters without mutating durable state; `lastErrorTruncated=true` marks truncation;
- source URLs, storage keys, source hashes/content and credentials are not joined or exposed;
- no mutation, retry/cancel control, dashboard, frontend store, Redis/cache/event stream, polling infrastructure, schema migration or new index was added; worker ownership, retry, lease and generation-fencing semantics are unchanged.

### Accepted local gate

Accepted uploaded evidence package:

`m5-jobs-observability-20261007T100646Z.zip`

Independently verified uploaded-file SHA-256:

`ca7bcd6a9e3d5cbaa4e31a2b30f78100d2755dc4bcb1c07bab013425ed3534c4`

ZIP integrity passed with 62 retained entries. The archive contains an embedded `zip.sha256` value `0e34941ee4bcbe56d18b94acc490d341a4dec0115662f05d4d1e1231e0006715`, which does not equal the final uploaded ZIP bytes because the gate packaging attempted to retain a checksum sidecar inside the archive. Acceptance uses the independently computed uploaded-file hash above plus direct inspection of the retained raw evidence; this packaging mismatch is not treated as product evidence.

The exact executable `08ed07455bd568914f1a23f5831823defaabb88a` passed all **47/47** live assertions in an isolated production-path gate:

- real signed-out request returned 401 `unauthenticated`; ordinary member returned 403 `forbidden`; moderator and admin sessions returned 200 through the real auth/store/API path;
- the disposable fixture contained 3,006 jobs, including new pending, pending retry, running, expired-lease running, failed and succeeded cases;
- a complete `limit=100` walk returned all 3,006 IDs strictly descending over 31 pages with zero duplicates or overlap; default limit was 50, `limit=200` clamped to 100, nonzero cursor chaining was coherent, exhaustion returned an empty page, and malformed/negative cursors returned 400;
- the 300-character worker ID projected to exactly 256 characters; the 2,100-character durable error projected to exactly 2,048 with `lastErrorTruncated=true`; short errors remained unmarked; PostgreSQL retained the original 300/2,100-character durable values;
- authoritative before/after snapshots of all 3,006 media jobs had identical SHA-256 `18aece64a1a4be6250e28e544cd3ce30a611d26d983f6b86ad4287c94681f927`, establishing read-only behavior across the GET traffic;
- retained API responses contained none of the seeded source-URL/storage-key/hash/credential secret markers;
- first-page `EXPLAIN (ANALYZE, BUFFERS)` used `Index Scan Backward using media_jobs_pkey`, returned the 51-row `limit+1` probe, bounded final quicksort to 30 kB, planning 0.465 ms and execution 0.109 ms;
- nonzero-cursor plan used the same backward primary-key scan with `Index Cond: (id < 4516)`, 30 kB bounded quicksort, planning 0.348 ms and execution 0.114 ms;
- neither plan sequentially scanned `media_jobs`, used an unbounded sort or spilled to temp; the only sequential scan was the tiny one-row disposable `user_roles` relation and does not justify an index change;
- `cargo test --locked` at the same exact SHA passed **71 tests, 0 failed**;
- cleanup stopped the isolated API, dropped and verified absence of the disposable database, removed the media root and detached worktree, preserved the shared database/services, and left the canonical tracked tree clean.

Decision: **accept the first M5 jobs/admin observability slice**. Existing indexes and the unfiltered immutable-ID cursor contract are sufficient for this slice; no jobs-state index, Redis dependency, cache, frontend dashboard or mutation surface is justified.

### Integration and post-integration CI

Before integration, live `v2` was:

`def1e1a99b57daa9b32776c1a7108292e7f1bcee`

The implementation/state branch head was 14 commits ahead and zero behind:

`d209de0d094cdfc2668fd9b4c8522356df3d5d9f`

The branch was fast-forwarded non-force to `v2` with an expected-SHA lease. GitHub compare confirmed the accepted executable candidate and the implementation branch differed only by `docs/v2/STATE.md`.

Post-integration `v2 CI`:

- run `37606990362`;
- job `112744781546`;
- exact integrated head `d209de0d094cdfc2668fd9b4c8522356df3d5d9f`;
- conclusion: **success**;
- scoped correctness, target-worker build applicability and tracked-clean checks all passed.

The executable application files at integration are identical to accepted candidate `08ed07455bd568914f1a23f5831823defaabb88a`. This final state update is documentation-only.

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

No unresolved correctness or SQL-plan blocker remains from the accepted second role-administration slice. The bootstrap-only role existence probe scans an empty fresh-install `user_roles` relation by design; current evidence does not justify a role-leading index.

No unresolved correctness, authorization, concurrency, lease-fencing or SQL-plan blocker remains from the accepted regeneration/admin mutation slice. The authorized mutation is bounded by existing indexes and requires no new index or cache.

## Single best next task

Run the **consolidated M5 moderation/admin/imports milestone acceptance gate** against exact executable application state `640311fc216435be90588484cbbc0624a097f598`. Verify the already-integrated M5 surfaces coherently through real PostgreSQL/API/worker execution: post/comment moderation, upload and URL ingestion, jobs/admin observability, moderator/admin role administration including bootstrap policy, and admin regeneration including coalescing/supersession/generation fencing. Preserve existing M4 connected-core behavior, retain raw correctness/state/SQL-plan evidence and cleanup proof, and do not modify source or broaden functionality. Use the local execution agent for this execution-only gate because it requires real PostgreSQL/API/worker processes beyond this session's execution environment.
