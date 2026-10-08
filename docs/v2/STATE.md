# Ginbar v2 state / handoff

Last updated: 2026-10-08
Phase: **M7 production hardening in progress; first native process/file limits candidate pending CI/review; nginx/KDF accepted and integrated**
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
- M5 moderation/admin/imports: **complete and integrated**.
  - first post/comment moderation slice: **accepted, integrated, and post-integration CI verified green after runner remediation**;
  - first imports HTTP/config slice: **accepted, integrated, exact-candidate CI green, and live local acceptance gate passed**;
  - first jobs/admin observability slice: **accepted, integrated, exact-candidate/local-gate/post-integration CI green**;
  - first role-administration slice: **accepted, integrated, exact-candidate/post-integration CI green**.
  - second role-administration/bootstrap slice: **accepted, integrated, exact-candidate/post-integration CI green**.
  - first regeneration/admin mutation slice: **accepted, integrated, exact-candidate/post-integration CI green**.
  - consolidated M5 moderation/admin/imports milestone gate: **accepted; M5 closed**.
- M6 private messages: **complete and integrated**.
  - first one-to-one private-messages backend foundation: **accepted and integrated; exact-candidate/post-integration CI green**.
  - second bounded conversation/inbox summaries backend slice: **accepted and integrated; exact-candidate/post-integration CI green**.
  - first authenticated inbox/direct-thread frontend slice: **accepted, browser-gated and integrated; exact-candidate/post-integration CI green**.
  - consolidated M6 private-messages milestone gate: **accepted; M6 closed**.
- M7 production hardening: **in progress**.
  - first native process/file resource-limits candidate: **implemented on branch; exact-candidate CI and independent acceptance pending; not integrated**.
  - bounded password-KDF admission control: **accepted, target-host profiled and integrated; exact-candidate/post-integration CI green**.
  - first v2 production nginx serving boundary: **accepted and integrated; exact-candidate/post-integration CI green; live target-host nginx unchanged**.

## M7 production hardening — bounded password-KDF admission accepted and integrated

Verified implementation base:

`a5906f294929356445123543a435262faf4a9b00`

This base was documentation/state-only. The accepted M6 executable beneath it was:

`310a6f046d2ad0f74d5855d829d7d1d2948a9486`

Implementation branch:

`astra/m7-kdf-admission`

Exact accepted executable candidate and integrated executable:

`90dc49e2bfae09f9b2a578162473a03818014717`

Exact-candidate `v2 CI`:

- run `37682501802`;
- job `113001863060`;
- exact `head_sha=90dc49e2bfae09f9b2a578162473a03818014717`;
- conclusion: **success**;
- Go formatting, `go vet ./...`, PostgreSQL-backed `go test -v -count=1 ./...`, target-worker release-build applicability, exact checkout and tracked-clean verification all passed;
- backend log contained **335 test/subtest RUN entries and zero failure markers**;
- all new KDF admission/config/service/HTTP tests passed;
- `v2-ci: PASS sha=90dc49e2bfae09f9b2a578162473a03818014717 scope=backend`.

Target-host validation evidence supplied for the exact candidate was independently inspected from the uploaded archive `kdf-admission-20261007T204010Z.zip`. The uploaded bytes have SHA-256:

`8ec2755f23aad3b894113331f6c4183cb7f3481910c60b5bb7f307bcf7c6cc44`

The archive passed ZIP integrity checks. Its raw logs independently prove the exact SHA, green exact-candidate CI, tracked-clean detached target checkout, commands, benchmark output, focused tests, host/runtime metadata, RSS measurements, concurrency measurements and cleanup. The local agent's archived findings named a different retained-ZIP SHA (`5dbbcd40c18f105bd8b75de9d6278a631ed01ce4713721a99bee335e1a207f83`); therefore acceptance relies on the independently inspected uploaded archive contents and GitHub evidence, not on that claimed archive checksum. This is an evidence-packaging provenance discrepancy, not an application defect.

### Accepted admission boundary

- Argon2id defaults remain unchanged at **64 MiB memory, one iteration, parallelism one**.
- Registration hashing and login verification share one auth-service-local admission boundary.
- Two bounded buffered channels use standard synchronization only: one bounds running KDF operations and one bounds queued/waiting requests.
- Accepted conservative default: **1 running KDF + 4 queued requests**.
- Runtime configuration is `GINBAR_AUTH_KDF_MAX_CONCURRENT` (positive integer) and `GINBAR_AUTH_KDF_MAX_QUEUED` (non-negative integer, including zero queue).
- Saturation happens before invitation or password-credential lookup for valid-shaped requests, so overload does not disclose credential/invitation validity.
- Saturation maps uniformly to HTTP **503** code `authentication_unavailable` for registration and login, distinct from invalid credentials without leaking credential validity.
- Waiting acquisition is context-aware; canceled waiters release queue capacity synchronously.
- Context is rechecked after credential/invitation lookup and immediately before Argon2 starts, preventing newly canceled requests from beginning KDF work.
- Once `x/crypto/argon2` starts it is synchronous/non-interruptible; the admitted operation completes and releases its running token afterward.
- KDF capacity is released before durable registration persistence or session creation.
- No worker pool, background goroutine, Redis state, distributed limiting, client-IP policy, lockout, CAPTCHA or general abuse framework was added.

### Accepted target-host measurements

Production-class target evidence used an Intel Core i7-7700, 8 hardware threads, Go 1.25.0, approximately 62 GiB RAM / 46 GiB available, with low pre-gate host load and no material overlapping workload.

Measured default 64 MiB / t=1 / p=1 behavior:

- warmed `BenchmarkPasswordHash`: **43.7 ms/op**;
- warmed `BenchmarkPasswordVerify`: **44.0 ms/op**;
- cold first hash/verify in the disposable probe: approximately **88–95 ms**;
- `BenchmarkPasswordVerifyParallel` at GOMAXPROCS=8: **11.25 ms/op** aggregate benchmark figure;
- limiter-only `BenchmarkKDFAdmission`: **44.97 ns/op, 0 B/op, 0 allocs/op**.

Measured memory/concurrency behavior:

- fresh process after one hash: approximately **68 MiB peak RSS**;
- after a subsequent verify before prior memory is reclaimed: approximately **134 MiB peak RSS**;
- serial reuse remained flat rather than accumulating;
- two concurrent KDFs peaked around **265–290 MiB**;
- four concurrent KDFs peaked around **320–326 MiB**;
- four serial verifies took approximately **209–233 ms** total;
- two-way concurrency took approximately **188–212 ms**, only about 1.1× wall-clock improvement while increasing per-request contention and RSS;
- four-way concurrency took approximately **119–141 ms**, improving throughput but materially increasing memory-bandwidth contention and per-request resource cost;
- probe goroutine count was **1 at baseline and 1 at completion** in both runs;
- no swap or major page faults occurred in the probe runs.

Correctness/resource validation passed:

- all 15 focused auth tests;
- stable HTTP 503 saturation behavior for login and registration;
- KDF runtime config default/override/invalid-value tests;
- the seven admission/service-admission tests repeated three times: **21/21 pass**;
- bounded running/queue capacity, cancellation release, capacity reuse and shared registration/login admission;
- no capacity leak, waiter growth, goroutine growth or cancellation defect observed.

The target host could not run `go test -race` because no C compiler was installed. The first attempt had CGO disabled and the explicit CGO retry failed with `gcc` absent. This is an environmental evidence limitation rather than a candidate defect; exact-candidate CI, deterministic channel-state assertions, repeated concurrency tests, zero-allocation limiter benchmarking and flat goroutine evidence are sufficient for this slice.

Decision: **accept the bounded password-KDF admission slice with the 1-running/4-queued default unchanged**. The target measurements do not justify increasing concurrency. Any future increase requires production-load burst/p99 latency and co-located RSS evidence.

### Integration verification

Immediately before integration, live `v2` was:

`a5906f294929356445123543a435262faf4a9b00`

The candidate was exactly four commits ahead with zero commits behind. Remote `v2` was non-force fast-forwarded with an expected-SHA lease to:

`90dc49e2bfae09f9b2a578162473a03818014717`

Post-integration `v2 CI`:

- run `37687463996`;
- job `113018860739`;
- branch `v2`;
- exact `head_sha=90dc49e2bfae09f9b2a578162473a03818014717`;
- conclusion: **success**;
- exact checkout, scoped backend correctness, target-worker release-build applicability and tracked-clean verification all passed.

The implementation-branch documentation commit was not integrated. The executable state above is authoritative for this slice.

## M7 production hardening — first v2 production nginx serving boundary accepted and integrated

Verified integration base:

`f5dcbd51db764b9ec73617a61051252ae6fb1c84`

This base was documentation/state-only. The accepted executable application state beneath it before the nginx slice was:

`90dc49e2bfae09f9b2a578162473a03818014717`

Implementation branch:

`astra/m7-nginx-boundary`

Exact accepted executable/configuration candidate and integrated executable:

`20be4765e935e715f2cd8862da0a6382e1322281`

The implementation branch has one documentation/state-only commit above the accepted candidate:

`d5b4dd36f256d1f40d6d6d653d825ad1576d7664`

That documentation-only tip was **not** integrated.

Exact-candidate `v2 CI`:

- run `37689584750`;
- job `113026029629`;
- exact `head_sha=20be4765e935e715f2cd8862da0a6382e1322281`;
- conclusion: **success**;
- full `scope=all` correctness passed: Rust worker formatting/check/tests/clippy, PostgreSQL-backed Go formatting/vet/tests, frontend 48/48 tests + typecheck + Vite build, target-worker release-build applicability, exact checkout, tracked-clean verification and the real-nginx fixture;
- real-nginx gate reported `v2-nginx-test: PASS https_port=32771 asset=/assets/index-CMeOteM1.js streaming_upstream_seen=true`.

Independent review evidence was supplied as `nginx-boundary-review-20261007T221447Z.zip`. The independently computed uploaded ZIP SHA-256 is:

`05984f225eb3818e96c569dec65a321873d69edd4be6ac8623462bf2d6772da6`

The archive was ZIP-clean and the review returned **ACCEPT** with no correctness, security-boundary, architecture, validation or scope blocker. Acceptance also independently reverified the candidate was exactly six commits ahead of the live integration base and zero behind, changing only `.github/workflows/v2-ci.yml`, `nginx/v2/nginx.conf`, `scripts/v2-ci.sh` and `scripts/v2-nginx-test.sh`.

### Accepted serving boundary

- v2 has a dedicated production nginx configuration; legacy Wallium/Fiber/Redis and legacy `/images`/`/videos` assumptions were not reused;
- nginx terminates TLS and redirects port 80 to HTTPS;
- the Vite application is served directly, content-hashed `/assets/` entries receive one-year immutable caching, and the SPA shell is explicitly `no-store`;
- canonical frontend routes fall back to `index.html`, preserving direct `/post/:id` and other client routes;
- only processed media under `/media/` is exposed from the media tree; ingestion `/sources/` is not aliased and is explicitly 404, with traversal behavior covered by the fixture;
- static processed media preserves normal nginx byte-range behavior; the fixture verifies 206 and exact `Content-Range`;
- `/api/` proxies to the Go v2 API without legacy services;
- the proxy preserves the original `Host` verbatim with `$http_host`, including an explicit non-default port, and sets `X-Forwarded-Proto $scheme`, matching the accepted same-origin mutation/auth contract;
- multipart upload is bounded at nginx to 257 MiB including framing headroom over the backend 256 MiB source cap, uses `proxy_request_buffering off`, and has bounded client/proxy timeouts;
- URL-import request bodies are bounded to 16 KiB and use the longer bounded ingestion timeout;
- other API request bodies are bounded to 1 MiB with shorter bounded proxy timeouts;
- the real-nginx container fixture builds the actual Vite frontend, loads the tracked nginx config with `nginx -T`, exercises TLS redirect/static/SPА/media/range/source-isolation/API-header/body-limit contracts, and proves a deliberately slow upload reaches the upstream before the request body finishes;
- CI path coverage now includes the v2 nginx config and nginx fixture, and nginx changes resolve to the full `scope=all` gate.

No per-IP rate limit, trusted-proxy/client-IP policy, WAF, system/process limit, PostgreSQL tuning, backup/recovery automation, deployment automation or broader observability was added in this slice. The live target-host nginx configuration was not modified or reloaded.

### Integration verification

Immediately before integration, live `v2` was:

`f5dcbd51db764b9ec73617a61051252ae6fb1c84`

The accepted candidate was exactly six commits ahead with zero commits behind. Remote `v2` was non-force fast-forwarded with an expected-SHA lease to:

`20be4765e935e715f2cd8862da0a6382e1322281`

Post-integration `v2 CI`:

- run `37698793880`;
- job `113056970226`;
- branch `v2`;
- exact `head_sha=20be4765e935e715f2cd8862da0a6382e1322281`;
- conclusion: **success**;
- exact checkout/SHA verification passed;
- the full `scope=all` correctness gate passed;
- post-integration real-nginx gate reported `v2-nginx-test: PASS https_port=32773 asset=/assets/index-CMeOteM1.js streaming_upstream_seen=true`;
- `v2-ci: PASS sha=20be4765e935e715f2cd8862da0a6382e1322281 scope=all`;
- target-worker release-build applicability passed;
- tracked checkout remained unchanged and all workflow steps completed successfully.

Decision: **accept and close the first v2 production nginx serving-boundary slice**. There is no known correctness, security-boundary, range, caching, upload-streaming, same-origin proxy or CI blocker from this slice. M7 production hardening remains in progress.

## M7 production hardening — first native process/file resource boundary awaiting acceptance

Verified live integration base: `544a4412141eacf6148daf4c74a1553fb9ab6b22` (documentation/state-only).
Accepted integrated executable/configuration beneath it: `20be4765e935e715f2cd8862da0a6382e1322281`.
Implementation branch: `astra/m7-service-limits`.
Exact executable/configuration candidate: `47f1a1b883898a33760a29117035fc7cd1897cd4`.
No integration or production deployment has been performed.

The existing `wallium.service` and scripts belong to the legacy Docker Compose deployment; v2 has native Go and Rust executables and a separate accepted nginx configuration, but no installed v2 service manager configuration. The smallest fitting initial serving model is therefore two standalone native systemd service units under `systemd/v2/`, without replacing the legacy service or adding installer/update automation.

- API: `/usr/local/bin/ginbar-api-v2`, `LimitNOFILE=4096`, `TasksMax=256`, `MemoryMax=1G`.
- Worker: `/usr/local/bin/ginbar-worker-v2 run`, `LimitNOFILE=1024`, `TasksMax=128`, `MemoryMax=4G`; tasks and memory account for child FFmpeg/FFprobe processes.
- Both: `User/Group=ginbar`, dedicated environment files under `/etc/ginbar/v2/`, group-wide kill, `OOMPolicy=kill`, `Restart=on-failure`, `RestartSec=10s`, and `StartLimitBurst=5` per five minutes. API shutdown timeout is 30 seconds, worker timeout 90 seconds.
- Existing media path shared with nginx: `/srv/ginbar/media`. No production host provisioning, service install/reload, or credentials are committed.
- `scripts/v2-service-limits-test.sh` builds an ephemeral Docker systemd PID 1 fixture on an isolated CI VM, verifies both units with `systemd-analyze verify`, observes applied process FD limits and actual cgroup `memory.max`/`pids.max`, starts both service paths using stand-in executables, and checks restart after forced API termination.
- The CI workflow now watches `systemd/v2/**` and the fixture; scoped CI treats such changes as `scope=all`. The application correctness suite and target-worker build remain separate checks.
- `systemd/v2/README.md` documents exact deployment prerequisites, test isolation and limitations.

CI status at this writing: **pending** for exact candidate `47f1a1b883898a33760a29117035fc7cd1897cd4`, workflow run `37732772249`, job `113165689879`. Previous superseded candidate runs were cancelled; neither success nor failure was inferred from them. Do not accept, integrate or hand this candidate to the local agent without completed green exact-candidate CI.

Outstanding evidence: tests must finish green; effective service startup/normal operation with **real** Go/Rust binaries and disposable PostgreSQL/media workloads remains untested under systemd. The 1 GiB/4 GiB caps are provisional containment, not measured peak-size recommendations. An independent execution gate on an isolated production-class host is required before acceptance. No target-host live deployment changes are authorized.

## M6 private messages — first backend foundation accepted and integrated

Verified implementation base:

`6609298210d45b30acff725649880394fd2c8736`

Implementation branch:

`astra/m6-private-messages`

Exact accepted executable candidate and integrated executable:

`8a716a035e4f6652d1666d18760eb6590a65b20b`

Exact-candidate `v2 CI`:

- run `37632978004`;
- job `112831784475`;
- exact `head_sha=8a716a035e4f6652d1666d18760eb6590a65b20b`;
- conclusion: **success**;
- scoped server correctness, Go formatting, `go vet ./...`, PostgreSQL-backed `go test -v -count=1 ./...`, target-worker release-build applicability, exact checkout, and tracked-clean verification all passed;
- candidate backend log contained **312 test/subtest RUN entries and zero failure markers**;
- `v2-ci: PASS sha=8a716a035e4f6652d1666d18760eb6590a65b20b scope=server`.

Post-integration `v2 CI`:

- run `37633250892`;
- job `112832701215`;
- exact integrated executable `8a716a035e4f6652d1666d18760eb6590a65b20b`;
- conclusion: **success**;
- scoped server correctness, target-worker release build, exact checkout, and tracked-clean verification all passed.

Earlier branch heads were not accepted. Initial CI exposed only Go formatting and test-fixture defects, then retained SQL-plan evidence exposed a real cursor-query risk where PostgreSQL could prefer the global message primary key and filter the pair. The accepted candidate fixes that query shape with bounded directional scans before integration.

### Accepted schema and service boundary

- migration `008_private_messages.sql` introduces one fresh `private_messages` relation; no legacy message schema was ported;
- durable identity is immutable numeric `users.id` only: `sender_user_id` and `recipient_user_id` are foreign keys, and usernames are not duplicated into relational identity;
- each message has immutable bigint identity, bounded body, and creation timestamp;
- self-messaging is explicitly **forbidden** both in the service boundary and by a database check constraint;
- message bodies are valid UTF-8, reject NUL, and contain **1 through 10,000 Unicode code points**; the migration defensively enforces the same character-count bound;
- recipients/peers must be existing active users under the current user-status model; inactive and missing users deliberately share the same unavailable result;
- no conversation table is introduced: the first direct-thread slice does not need mutable conversation state, and the simpler message relation avoids speculative group-chat abstractions;
- the single concrete thread index is `private_messages_thread_idx(sender_user_id, recipient_user_id, id DESC)`, matching actual directional thread scans.

### Accepted HTTP/API contract

- direct thread endpoint is `GET /api/v2/messages/{peerId}`;
- direct send endpoint is `POST /api/v2/messages/{peerId}`;
- both require the established authenticated session; sender/actor identity comes only from the session principal's immutable numeric user ID;
- the request body cannot supply or override sender identity; unknown fields are rejected;
- POST uses the established same-origin mutation policy and rejects cross-origin requests before persistence;
- malformed/non-positive peer identity returns 400 `invalid_peer_id`;
- missing/inactive recipient or peer returns 404 `recipient_unavailable`;
- self-send/self-thread returns 409 `self_message_forbidden`;
- invalid, empty, NUL-containing, malformed or oversized bodies return the existing bounded 400 request/body error conventions;
- internal failures retain the existing 500 `internal` contract and request cancellation/deadline behavior remains inherited from the server boundary;
- repeated identical sends are distinct messages; no idempotency key or deduplication mechanism was invented;
- successful send returns the authoritative inserted `id`, numeric sender/recipient IDs, body and `createdAt` directly from PostgreSQL `RETURNING`; there is no application-side reconciliation read.

### Accepted thread and pagination behavior

- a thread contains only messages where authenticated user A and numeric peer B are the sender/recipient pair in either direction;
- unrelated A↔C and B↔C traffic is excluded;
- ordering is deterministic **message ID descending**;
- cursor parameter is `before=<message-id>`, with no OFFSET;
- default page size is **50**, hard maximum **100**;
- the store reads `limit+1` to determine continuation; `nextBefore` is the last returned immutable message ID;
- cursor pages are unique, non-overlapping and exhaust cleanly;
- the SQL path performs two bounded directional scans—A→B and B→A—through the same composite thread index, combines them with `UNION ALL`, and keeps only the bounded merged page. This avoids the rejected global-primary-key cursor shape and requires no application-side filtering or N+1 work.

### Correctness and PostgreSQL evidence

Focused exact-candidate coverage passed for:

- signed-out send/read rejection;
- same-origin send enforcement with no durable write on cross-origin rejection;
- authenticated successful send with session-derived numeric sender identity;
- numeric sender/recipient persistence and rejection of request-body sender override;
- invalid numeric peers, missing recipients, inactive recipients and explicit self-message policy;
- malformed, empty, NUL-containing, oversized and Unicode boundary bodies;
- pair-only thread visibility with no unrelated-message leakage;
- deterministic descending ordering, default/max limits, cursor pagination, exhaustion, uniqueness and zero overlap;
- 12 concurrent identical sends creating exactly 12 distinct immutable IDs and exactly 12 durable rows;
- authoritative send response matching the inserted durable row;
- defensive database checks for self-messages and oversized rows;
- real authenticated HTTP send/read against PostgreSQL;
- preservation of the existing backend/PostgreSQL suite.

Retained `EXPLAIN (ANALYZE, BUFFERS)` on a meaningful fixture containing roughly 25,000 messages plus 6,000 extra users showed:

- first thread page: `users_pkey` for active-peer validation plus two `private_messages_thread_idx` scans under a bounded `Merge Append`; returned 51 probe rows, used only shared-hit buffers, 28 kB in-memory outer quicksort, planning **1.027 ms**, execution **0.243 ms**;
- cursor thread page: `users_pkey` plus two `private_messages_thread_idx` scans including the `id < cursor` condition; returned 51 probe rows, 28 kB in-memory outer quicksort, planning **0.301 ms**, execution **0.254 ms**;
- send path: active-recipient lookup through `users_pkey`, one insert with authoritative `RETURNING`, planning **0.043 ms**, execution **0.286 ms** including foreign-key triggers;
- no large-relation sequential scan, OFFSET, external merge, disk/temp spill, application-side filtering, reconciliation query, or N+1 behavior was present.

Decision: **accept the first M6 private-messages backend foundation**. PostgreSQL remains authoritative; numeric identity, bounded direct-thread paging, send concurrency, same-origin mutation safety and actual SQL plan shapes are proven by exact-candidate CI. No local execution-agent gate is required for this backend/PostgreSQL-only slice.

### Integration status

Immediately before integration, live `v2` was:

`6609298210d45b30acff725649880394fd2c8736`

Remote `v2` was fast-forwarded non-force with expected-SHA lease to exact executable:

`8a716a035e4f6652d1666d18760eb6590a65b20b`

Post-integration CI is green as recorded above. No frontend messaging UI, inbox/conversation-summary query, unread/read state, attachment/group/system-message abstraction, notification mechanism, polling/SSE/WebSocket path, Redis/cache state, or messaging moderation/admin control was added.

M6 remains **in progress**; only the first direct-thread backend foundation is closed.


## M6 private messages — bounded inbox summaries accepted and integrated

Verified implementation base:

`78f83eaa2151facb5fe49c84d586b2057a62b4e9`

This base was documentation/state-only; its accepted executable parent was:

`8a716a035e4f6652d1666d18760eb6590a65b20b`

Implementation branch:

`astra/m6-inbox-summaries`

Exact accepted executable candidate and integrated executable:

`f6b918654b04f867070338dee2390908db9bf8dd`

Exact-candidate `v2 CI`:

- run `37667045577`;
- job `112948834540`;
- exact `head_sha=f6b918654b04f867070338dee2390908db9bf8dd`;
- conclusion: **success**;
- Go formatting, `go vet ./...`, PostgreSQL-backed `go test -v -count=1 ./...`, schema coverage, target-worker release-build applicability, exact checkout, and tracked-clean verification all passed;
- candidate backend log contained **321 test/subtest RUN entries and zero failure markers**;
- `v2-ci: PASS sha=f6b918654b04f867070338dee2390908db9bf8dd scope=backend`.

Post-integration `v2 CI`:

- run `37667381457`;
- job `112949956705`;
- exact integrated executable `f6b918654b04f867070338dee2390908db9bf8dd`;
- conclusion: **success**;
- scoped correctness, target-worker release build, exact checkout, and tracked-clean verification all passed.

An earlier exact candidate `6b1efdf2cd6f648bcb91771c8278c2bb95985113` was **not accepted**. CI run `37666783319`, job `112948031198`, exposed a real plan defect: PostgreSQL attached peer metadata with a hash join that sequentially scanned all 6,401 fixture users for a 51-row inbox page. The accepted executable replaces that join with a bounded lateral primary-key lookup before integration.

### Accepted schema and authoritative update path

- migration `009_private_message_inbox.sql` introduces `private_message_conversations`, a minimal canonical one-row-per-user-pair relation keyed by ordered immutable numeric user IDs;
- the relation stores only `user_low_id`, `user_high_id`, and authoritative `latest_message_id`; it does **not** duplicate usernames, bodies, timestamps, unread state, or speculative UI state;
- migration backfill derives one row per existing pair from `private_messages` with `max(id)`, so messages created by the accepted first M6 slice immediately participate in inbox summaries;
- `private_message_conversations_low_latest_idx(user_low_id, latest_message_id DESC, user_high_id)` and `private_message_conversations_high_latest_idx(user_high_id, latest_message_id DESC, user_low_id)` exist only for the actual inbox query shape;
- the accepted direct-thread index `private_messages_thread_idx(sender_user_id, recipient_user_id, id DESC)` is preserved unchanged;
- successful sends insert the authoritative `private_messages` row and update the canonical pair's `latest_message_id` in one PostgreSQL statement;
- the conflict path uses `GREATEST(existing.latest_message_id, excluded.latest_message_id)`, so concurrent/opposite-direction sends cannot let an older immutable message ID overwrite a newer one because of transaction completion order;
- send responses still come directly from the inserted PostgreSQL row; there is no reconciliation read, trigger-maintained summary, background aggregation job, Redis/cache state, or application-side grouping.

Decision: the small canonical conversation relation is justified by measured inbox requirements. Deriving latest-per-peer summaries directly from unbounded message history would make inbox work grow with historical messages; retaining one authoritative latest-message pointer per pair makes read work depend on conversation summaries instead.

### Accepted inbox/API and cursor contract

- new endpoint is authenticated `GET /api/v2/messages`; existing `GET /api/v2/messages/{peerId}` and `POST /api/v2/messages/{peerId}` behavior is preserved;
- inbox actor identity comes exclusively from the session principal's immutable numeric `users.id`;
- each result contains exactly one peer summary and authoritative latest-message identity sufficient for ordering/rendering: peer numeric ID, current username only when currently active, availability, latest message ID, latest sender numeric ID, and latest timestamp;
- usernames are joined current presentation metadata only and never relational or authorization identity;
- inactive peers remain represented as an unavailable tombstone with numeric peer ID and latest-message state but no username; the existing direct-thread availability contract remains 404 `recipient_unavailable`;
- valid durable state cannot have a physically missing peer because message/conversation user foreign keys prevent deletion while rows reference that user;
- ordering is deterministic **latest immutable message ID descending**;
- cursor is `before=<latest-message-id>`; cursor reads consider only current conversation summaries whose latest message ID is lower than the cursor;
- default page size is **50**, hard maximum **100**, store reads `limit+1`, and `nextBefore` is the last returned summary's latest message ID;
- sequential pages are unique and non-overlapping and exhaust cleanly;
- pagination is intentionally non-snapshot: if a conversation receives a newer message after an earlier page was read, it can move above that old cursor and therefore be omitted from continuation below the old cursor, while a fresh first page immediately reflects the new authoritative latest message/order;
- no OFFSET, pagination session/snapshot state, unread/read receipt, notification, polling, SSE/WebSocket, or Redis state was introduced.

### Correctness and PostgreSQL evidence

Focused exact-candidate coverage passed for:

- signed-out inbox rejection and session-derived numeric actor identity;
- empty inbox;
- one summary per peer despite multiple messages in both directions;
- unrelated-user conversation isolation;
- deterministic latest-message-ID descending ordering;
- current username presentation metadata without username relational identity;
- inactive-peer tombstone behavior and preservation of direct-thread unavailability semantics;
- default/max page bounds, cursor exhaustion, uniqueness and zero overlap;
- fresh-read reordering after a new send;
- concurrent sends across multiple peers with exactly one durable canonical row per pair and authoritative maximum latest message ID;
- stable HTTP internal-error handling;
- migration/backfill/index assertions;
- preservation of accepted direct-thread send/read tests and the existing PostgreSQL/backend suite.

Retained `EXPLAIN (ANALYZE, BUFFERS)` used a meaningful fixture with 200 lower-ID peers, the actor, 200 higher-ID peers, 6,000 unrelated users, roughly 8,000 actor↔peer messages, and roughly 15,000 unrelated messages:

- first inbox page: both canonical directional indexes participated under a bounded `Merge Append`; latest-message lookup used `private_messages_pkey` and peer metadata used `users_pkey` through the accepted lateral lookup, each for the bounded returned page; no large-relation sequential scan occurred; execution **0.381 ms**;
- cursor inbox page: both canonical directional indexes handled actor/cursor predicates, feeding bounded top-N heapsorts using **28 kB** each in the retained fixture; latest messages and peer metadata were primary-key lookups for 51 probe rows; no message-history scan, large-relation sequential scan, external merge, temp/disk spill or OFFSET occurred; planning **0.293 ms**, execution **0.526 ms**;
- inbox-aware send: recipient validation used `users_pkey`, the message insert remained authoritative, and the canonical pair update used `private_message_conversations_pkey` conflict resolution; no reconciliation query or large-relation sequential scan occurred; planning **0.153 ms**, execution **0.514 ms** including foreign-key triggers.

The initially rejected peer-metadata plan scanned 6,401 users and was removed before acceptance. The accepted first-page plan instead executed 51 bounded `users_pkey` lookups. The actual query/store path contains no application-side peer filtering/grouping and no application-side N+1 query pattern.

Decision: **accept the second M6 bounded conversation/inbox summaries backend slice**. PostgreSQL remains authoritative; the concrete durable summary relation and its two directional indexes are justified by the measured query shape. No local execution-agent gate is required for this backend/PostgreSQL-only slice.

### Integration status

Immediately before integration, live `v2` was still the verified documentation/state-only base:

`78f83eaa2151facb5fe49c84d586b2057a62b4e9`

Remote `v2` was fast-forwarded non-force with an expected-SHA lease to exact executable:

`f6b918654b04f867070338dee2390908db9bf8dd`

Post-integration CI is green as recorded above.

M6 remains **in progress**. The direct-thread backend foundation and bounded inbox-summary backend are closed; frontend messaging UX and later explicitly justified messaging features remain.


## M6 private messages — first frontend slice accepted and integrated

Verified implementation base:

`206bdb24ba483adbab26c121647d90158b1f9609`

That base was documentation/state-only; its accepted executable parent was:

`f6b918654b04f867070338dee2390908db9bf8dd`

Implementation branch:

`astra/m6-messaging-frontend`

Exact accepted executable candidate and integrated executable:

`310a6f046d2ad0f74d5855d829d7d1d2948a9486`

Exact-candidate `v2 CI`:

- run `37669465599`;
- job `112957170929`;
- exact `head_sha=310a6f046d2ad0f74d5855d829d7d1d2948a9486`;
- conclusion: **success**;
- frontend correctness gate passed **48/48 tests**, `tsc --noEmit`, production Vite build, target-worker release-build applicability, exact checkout and tracked-clean verification;
- `v2-ci: PASS sha=310a6f046d2ad0f74d5855d829d7d1d2948a9486 scope=frontend`.

Accepted browser/DevTools evidence:

`m6msg-20261007T185051Z.zip`

Independently verified SHA-256:

`2a6c7e4aa3abb700e87f456467e5e27583c93037035d6ecbb17d912a54c7b730`

Archive integrity passed. Raw scenario JSON, request logs, screenshots, Playwright traces, failure classification and cleanup evidence were inspected. The five browser scenarios contained **47 checks with zero failures**. The only transport failures were one intentional stale-read `ERR_ABORTED` and two deliberately forced send failures; there were zero page errors.

Post-integration `v2 CI`:

- run `37673153104`;
- job `112969758771`;
- exact integrated executable `310a6f046d2ad0f74d5855d829d7d1d2948a9486`;
- conclusion: **success**;
- exact checkout, scoped frontend correctness, target-worker release-build applicability and tracked-clean verification all passed.

### Accepted frontend boundary and routing

- canonical inbox route is `/messages`; canonical direct-thread route is `/messages/:peerId`;
- route, request and retained-thread identity use immutable numeric peer IDs only; usernames remain current presentation metadata;
- direct numeric thread navigation works without requiring an inbox fetch; when no current username metadata is loaded, the deterministic heading fallback is `User #<id>`;
- signed-out messaging routes use the established `/api/v2/auth/me` session boundary and expose no message data;
- the signed-in board exposes a Messages entry while existing board and profile document routing remains separate;
- invalid nonnumeric message routes render an explicit invalid-route state rather than being interpreted as username identity;
- no router framework, giant application store, service worker, client database or second auth model was introduced.

### Accepted retained state, pagination and stale-response behavior

- inbox pages use only the accepted `before=<latest-message-id>` cursor and preserve authoritative latest-message-ID descending order;
- retained inbox summaries are capped at **200**; reaching the cap removes older loading and shows an explicit retention note;
- direct threads use only `nextBefore` for older pages; retained messages are kept in immutable message-ID descending server order and derived ascending for display;
- each retained thread is capped at **300 messages** and the messaging surface retains at most **4 thread states**;
- no OFFSET pagination or snapshot machinery is present;
- inbox append deduplicates by numeric peer ID, and fresh first-page refresh allows a newly updated conversation to move to its new authoritative position;
- thread/inbox reads use AbortController plus route epochs; a delayed peer-A response released after switching to peer B cannot mutate B;
- Back/Forward and rapid peer switching keep URL, heading and rendered message set coherent;
- the retained-state bounds were intentionally chosen before virtualization; current evidence does not justify virtualization.

### Accepted send behavior

- composer bodies remain bounded to the backend's **10,000 Unicode-code-point** limit;
- at most **8 local pending sends** are retained;
- submitting shows immediate local pending state but does not grant durable client authority;
- each successful POST response is merged exactly once by authoritative immutable message ID without refetching the whole thread;
- out-of-order rapid send responses settle into deterministic authoritative message-ID order without duplicates;
- failed sends preserve confirmed retained thread state;
- an eligible failed body may restore into an empty draft, but cannot overwrite a newer user draft;
- recipient-unavailable send failure switches the peer to an unavailable presentation state and disables the composer;
- no polling, SSE/WebSocket path, unread/read receipts, notifications, attachments, reactions, editing/deletion, groups or messaging moderation/admin controls were added.

### Browser fixture and measured behavior

The isolated real-browser gate used PostgreSQL 16.15 with migrations 001–009, real API registration/login/send paths, nginx/static frontend serving and Chromium. The fixture contained 211 actor conversations and 778 seeded messages before gate-time bump sends, including bidirectional traffic, an inactive-peer tombstone, an empty-inbox actor, a 310-message thread, 75- and 60-message threads, and enough additional peers to force multiple inbox pages.

Accepted checks established:

- inbox pages were latest-message-ID descending and cursor chained with no OFFSET; 200 summaries were retained at the explicit cap;
- unavailable peer `#63` rendered as a non-clickable tombstone without fabricated username;
- fresh refresh after a send moved the conversation to authoritative rank 1;
- username rename affected only presentation while numeric route/API identity stayed stable;
- a 310-message thread retained exactly 300 unique messages and stopped at the client cap with the retention note;
- signed-out inbox/direct-thread routes made zero message-API requests;
- stale peer response isolation, rapid route switches and Back/Forward all passed;
- successful pending-to-authoritative reconciliation inserted one DOM message node and did not refetch the thread;
- three rapid sends released in reverse response order reconciled to unique authoritative IDs;
- forced send failure, newer-draft preservation and recipient-unavailable behavior all passed;
- existing board route, missing-post route and public profile route remained coherent in the exercised fixture.

Browser-machine measurements:

- warmed inbox interaction: **36 ms**;
- warmed populated-thread switch: **16 ms**;
- older thread append: **13 ms**;
- older inbox append: **14 ms**;
- un-delayed send reconciliation: **287 ms**, with pre-existing message DOM nodes retained;
- Back/Forward measurements were approximately **722/714 ms**, explicitly sleep-dominated upper bounds rather than interaction latency claims;
- JS heap grew from about **11.5 MB to 15.9 MB** across eight thread visits, about **+4.4 MB**, without a single-visit spike;
- zero browser page errors were recorded.

`PerformanceObserver(type=longtask)` was unsupported in this headless Chromium, so no Long-Task count is claimed. Playwright traces were retained instead. The browser fixture intentionally had zero board posts, so thumbnail churn was not directly observable in this dedicated gate; messaging node stability and request isolation were proven, but the consolidated M6 gate should use a populated board/media fixture when practical.

Decision: **accept the first M6 authenticated inbox/direct-thread frontend slice**. Numeric identity, bounded retained state, cursor paging, stale-response isolation, immediate/authoritative send reconciliation and measured browser behavior satisfy the slice. Current evidence does not justify a frontend state framework, virtualization, polling/event streaming, Redis/cache synchronization, unread/read state or additional messaging abstractions.

### Integration status

Immediately before integration, live remote `v2` was the verified documentation/state-only base:

`206bdb24ba483adbab26c121647d90158b1f9609`

The candidate was exactly 10 commits ahead and zero behind. Remote `v2` was fast-forwarded non-force with expected-SHA lease to:

`310a6f046d2ad0f74d5855d829d7d1d2948a9486`

Post-integration CI is green as recorded above.

M6 remains **in progress** pending one consolidated milestone gate across the accepted backend/inbox/frontend messaging slices.


## M6 consolidated private-messages milestone gate — accepted; M6 complete

Exact executable validated:

`310a6f046d2ad0f74d5855d829d7d1d2948a9486`

Live `v2` at gate start was documentation/state-only:

`bf53fa30036566aaf57667e427aed44872c0c19b`

Applicable executable CI remained green:

- exact-candidate `v2 CI`: run `37669465599`, job `112957170929`, exact SHA `310a6f046d2ad0f74d5855d829d7d1d2948a9486`, success;
- post-integration `v2 CI`: run `37673153104`, job `112969758771`, exact executable SHA `310a6f046d2ad0f74d5855d829d7d1d2948a9486`, success.

Accepted retained execution evidence:

- ZIP: `m6gate-20261007T193138Z.zip`;
- authoritative SHA-256: `9be6437290573684038135fa00a811be4154d277fe28cb2b817f2e33febced0e`;
- size: 2,486,201 bytes;
- archive integrity check passed with no compressed-data errors;
- primary-assistant review verified the uploaded ZIP hash exactly and inspected the raw contracts, browser summaries, SQL plans, traces/screenshots inventory and cleanup evidence.

### Consolidated correctness and browser evidence

The gate used real PostgreSQL, the real Go API, the built SolidJS frontend, nginx and headless Chromium against a disposable fixture with 227 users, 212 actor conversations at seed, bidirectional long threads, an inactive-peer tombstone, 40 released SFW posts with real served AVIF media/thumbnails, tags, a populated profile and unrelated traffic.

All 50 raw API contract assertions passed:

- 19/19 authentication, origin, validation and boundary assertions;
- 21/21 inbox/thread pagination, ordering, tombstone, isolation and durable-send assertions;
- 10/10 concurrent-send assertions, including 100 successful concurrent sends with unique immutable IDs and exactly one canonical conversation row per pair whose `latest_message_id` equaled the true durable maximum.

All required browser assertions passed across inbox/thread, send/tombstone, stale-navigation/retention, board/profile and signed-out phases. Supplemental browser files contain overlapping phase probes, so no synthetic aggregate assertion count is used here. The accepted evidence demonstrated:

- exact 200-summary inbox retention and exact 300-message thread retention of the newest window;
- four-thread retained-state eviction;
- stale-response isolation under deliberately held real requests;
- coherent rapid route switching and Back/Forward;
- immediate pending-send presentation, exactly-once authoritative reconciliation and no whole-thread refetch;
- deterministic reconciliation of deliberately out-of-order send responses;
- failed-send preservation/restoration behavior and recipient-unavailable transitions;
- numeric route identity surviving username changes;
- signed-out message routes issuing no private-message API reads and exposing no private bodies;
- a populated board/profile/search/vote/keyboard regression tour with no board row or thumbnail-source churn attributable to messaging.

No application defect or concrete missing M6 requirement was found. The consolidated scope review found no requirement for unread/read receipts, notifications, polling, SSE/WebSockets, Redis synchronization, attachments, reactions, rich text, message editing/deletion, typing indicators, presence, groups, message search or messaging-specific moderation/admin controls.

### Consolidated SQL/performance evidence

Raw retained `EXPLAIN (ANALYZE, BUFFERS)` plans for the accepted hot shapes remained sub-millisecond in the disposable fixture:

- inbox first page: about **0.482 ms** execution in the retained raw plan;
- inbox cursor page: about **0.438 ms**;
- thread first page: about **0.114 ms**;
- thread cursor page: about **0.176 ms**;
- send plus canonical conversation upsert: about **0.428 ms** including FK-trigger work;
- forced no-sequential-scan inbox probe: about **0.188 ms**, confirming the accepted index-backed alternative.

The small-fixture planner chose small-relation sequential scans for latest-message/user probes in some normal plans; the retained no-sequential-scan probe demonstrated index capability. No OFFSET, external/disk sort, temp spill, history-wide aggregation, application-side grouping/filtering, reconciliation query or N+1 behavior was found, and the evidence does not justify a new index.

Measured warmed browser transitions remained in the tens of milliseconds where directly measured: inbox settle about 62 ms, populated-thread switch about 24 ms, board settle about 64 ms, board→inbox about 35 ms and messaging→board about 31 ms. Append, held-send and some Back/Forward measurements intentionally included scripted waits and are retained only as upper bounds, not interaction-latency claims. JS heap was about 5.3 MB on the capped inbox. The environment exposed the Long Tasks observer but the probe did not provide a meaningful retained Long-Task count; traces are retained instead.

### Cleanup and decision

Cleanup evidence shows temporary API/nginx processes stopped, gate ports free, disposable PostgreSQL state dropped, detached worktree removed, temporary build/media/config scratch deleted, unrelated workloads untouched and the canonical tracked checkout left clean.

Decision: **accept the consolidated M6 private-messages milestone gate and close M6**. The accepted one-to-one messaging product is coherent across PostgreSQL/API/frontend boundaries, bounded in retained client state, preserves established board/profile behavior, and has no measured correctness, security, SQL-plan or browser-performance issue requiring additional messaging architecture.

M7 production hardening is now the active milestone.

## M5 consolidated moderation/admin/imports milestone gate — accepted; M5 complete

Exact executable tested:

`640311fc216435be90588484cbbc0624a097f598`

Gate-time live `origin/v2`:

`9b4a71feb034b9655b41de4689c0af97d75a491c`

The live integration branch differed from the executable only by the two accepted regeneration state/documentation commits; no application drift was present.

Applicable CI verified in the retained evidence:

- exact-candidate `v2 CI` run `37618638054`, job `112783313294`, exact SHA `640311fc216435be90588484cbbc0624a097f598`: **success**;
- post-integration `v2 CI` run `37619197254`, job `112784890365`, exact integrated head `43b8fea82bb10202170dce20c5b396759bf7f05b`: **success**.

Accepted evidence package:

`m5gate-m5-20261007T122538Z.zip`

Independently verified uploaded-file SHA-256:

`d30b55b5312f391d8faf2f643163e25e981a7452cf71cfd8bbd4ee358c59dcb1`

ZIP integrity passed with 12 retained files. Raw HTTP/PostgreSQL state, worker output, SQL plans, CI metadata, full backend test output, driver source and cleanup findings were inspected. The retained gate recorded **116/116 live checks passed** with zero failures. The full backend log contains **291 test/subtest RUN entries and zero FAIL markers**.

### Consolidated gate facts

- Exact detached worktree HEAD matched `640311fc216435be90588484cbbc0624a097f598` and tracked status was clean.
- Representative M4 connected-core behavior remained coherent: invitation registration, login/session continuity/logout, signed-out auth boundary, descending cursor feed, search validation, around-post reconstruction, post voting, nested comment create/read/vote, tag mutation and media-status reads all passed.
- Post/comment moderation preserved PostgreSQL-authoritative moderator/admin authorization, same-origin enforcement, idempotence, moderation attribution/tombstoning and immediate feed exclusion.
- Multipart ingestion returned authoritative post/job identity, created the expected unreleased post/source/pending-job state, and the real Rust worker consumed that durable job at explicit concurrency 1. The worker completed successfully in about **376.5 ms**, published a real 285-byte AVIF under the disposable media root, moved the job to succeeded with one attempt and published the media row.
- URL-ingestion rejection behavior remained correct: loopback was rejected as `unsafe_url` with no durable row; malformed/missing URLs returned the accepted errors. The consolidated environment had no usable global-unicast HTTP source, so it could not re-exercise a positive live URL fetch. This is not an acceptance blocker because the dedicated accepted M5 imports gate already exercised `https://example.com/` through the live success path with HTTP 201 while preserving the SSRF boundary.
- Jobs observability retained signed-out/member rejection, moderator access, bounded cursor paging, 100-row limit clamping, 2048-character diagnostic truncation and read-only durable state.
- Moderator/admin role administration retained signed-out/member/moderator rejection, numeric-ID role reads/grants/revokes, immediate authority gain/loss, idempotent revoke, missing-target behavior, self-admin-revoke rejection, other-admin revoke and first-admin bootstrap one-shot/provenance semantics.
- Regeneration retained signed-out/member/moderator/cross-origin rejection with no mutation, stable non-regenerable behavior, succeeded/failed requeue, pending coalescing with retry/availability/error/generation state preserved, running supersession with generation increment and ownership clearing, unchanged published media, and stale-worker fencing: the old ownership token's completion attempt affected **0 rows**.
- Twelve concurrent regeneration requests returned twelve HTTP 202 responses with one immutable job ID, exactly **1 queued + 11 coalesced** outcomes and exactly one active durable job.
- Long-lived worker `run`-mode lease renewal was not re-exercised in this consolidated environment. This is not an M5 blocker: the gate exercised a real worker ownership/publication path and directly proved the regeneration interaction relevant to M5 by stale-generation fencing. No change to worker renewal semantics was introduced by M5.
- The full backend suite passed at the exact executable with no failures, preserving the broader authorization, ingestion, worker/media-job, role, moderation, tag and regeneration contracts.

### Consolidated SQL/performance evidence

On the retained meaningful fixture of roughly 6,000 role rows and 5,000 posts/sources/media/jobs:

- authorized regeneration used `user_roles_pkey`, `media_jobs_post_kind_id_idx`, `posts_feed_released_idx`, `media_sources_pkey`, `media_pkey` and `media_jobs_pkey`; the only target-preference sort was an in-memory 25 kB quicksort; planning was **0.628 ms**, execution **0.319 ms**;
- jobs observability used `user_roles_pkey` plus backward `media_jobs_pkey` scan with the bounded page probe and a 30 kB in-memory quicksort; planning was **0.324 ms**, execution **0.119 ms**;
- role grant used `user_roles_pkey` and `users_pkey`; planning was **0.328 ms**, execution **0.269 ms**;
- no large-relation sequential scan, external merge, temp spill, OFFSET, application-side reconciliation or newly exposed N+1 pattern was found.

### Cleanup and decision

The gate dropped and verified absence of disposable PostgreSQL databases, removed temporary media roots/binaries/processes and the detached worktree, pruned the worktree, left the pre-existing PostgreSQL cluster running, and reported the canonical tracked checkout clean. No tracked source, SQL, docs, config, commit, branch, deployment or shared persistent-state write was made by the execution agent.

Decision: **accept the consolidated M5 moderation/admin/imports milestone gate and close M5**. The integrated M5 surfaces work coherently with the retained M4 connected core, PostgreSQL remains authoritative across authorization and durable jobs, regeneration preserves lease-generation fencing and published-media semantics, hot SQL remains bounded/index-backed, and the two environment limits above do not expose a candidate defect or missing M5 acceptance requirement. No new cache, Redis dependency, schema/index change, frontend administration layer or worker-concurrency change is justified by this gate.

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
- the accepted v2 production nginx proxy uses `$http_host` rather than `$host`, preserving an explicit non-default port for the backend same-origin check, and sets `X-Forwarded-Proto $scheme` at the TLS boundary.

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
- broader per-client/distributed abuse controls, proxy-IP policy and Redis-backed limiting until measured need/topology requires them;
- virtualization, Redis synchronization, event streams or a large frontend store;
- v1-v2 end-to-end speedup claims before an apples-to-apples benchmark exists;
- profile edit/bio/avatar/display-name/rename functionality until a later explicit product slice requires it.

## Unresolved issues

No unresolved correctness, SQL-plan, browser-performance, integration or architecture blocker remains from M4 after the accepted consolidated milestone gate.

The profile older-cursor plan can inspect filtered author rows before finding an eligible SFW/ready row. Current 100,000-post evidence remains small and index-backed; do not add a partial profile index without a real distribution/latency signal that justifies it.

The profile browser gate's media 404 console messages came from benchmark storage keys without corresponding media files in the disposable nginx tree. This did not affect profile route/API/state assertions and is not a candidate defect, but future consolidated browser gates should use real served fixture media when practical so console-noise checks are cleaner.

No unresolved correctness or SQL-plan blocker remains from the accepted second role-administration slice. The bootstrap-only role existence probe scans an empty fresh-install `user_roles` relation by design; current evidence does not justify a role-leading index.

No unresolved correctness, authorization, concurrency, lease-fencing or SQL-plan blocker remains from the accepted regeneration/admin mutation slice. The authorized mutation is bounded by existing indexes and requires no new index or cache.

No unresolved M5 milestone blocker remains after the accepted consolidated gate. The gate environment could not provide a global-unicast source for a second live positive URL-import run and did not repeat long-lived worker run-mode renewal; both are explicitly covered by previously accepted dedicated behavior or unchanged worker semantics and do not justify reopening M5.

No unresolved correctness, authorization, concurrency, schema or SQL-plan blocker remains from the accepted first M6 private-messages backend foundation. The rejected pre-acceptance cursor plan that could scan the global message primary key was replaced before acceptance by two bounded directional composite-index scans. No additional message index or conversation table is justified by current query shapes.

No unresolved correctness, authorization, concurrency, schema or SQL-plan blocker remains from the accepted second M6 inbox slice. The first inbox candidate's peer-metadata sequential scan was rejected and replaced by bounded primary-key lookups before acceptance. The accepted cursor plan uses the canonical conversation indexes and bounded top-N heapsorts with no spill; no additional inbox index, cache, event stream, unread state or aggregation machinery is justified by current evidence.

No unresolved correctness, navigation, stale-response, retained-state or browser-performance blocker remains from M6. The consolidated gate exercised populated board/profile/media state, verified zero messaging-induced board row/thumbnail churn, retained browser traces, revalidated the API/SQL contracts and found no concrete missing M6 capability. The dedicated frontend gate limitations are therefore closed as milestone blockers.

No unresolved correctness, admission-capacity, cancellation, HTTP-semantics, allocation or target-resource blocker remains from the accepted M7 password-KDF admission slice. The production-class target supports the conservative 1-running/4-queued default; measured 2-way/4-way KDF concurrency increased RSS and memory-bandwidth contention enough that no higher default is justified. The target lacked a C compiler for `-race`; this remains an evidence-environment limitation, not an application blocker. The uploaded evidence ZIP hash differs from the local-agent-reported retained hash even though its raw contents are internally consistent and ZIP-clean; future evidence packaging should return the checksum of the exact transferred archive without post-hash repacking.

No unresolved correctness, security-boundary, cache-policy, range, source-exposure, proxy-header, upload-streaming or CI blocker remains from the accepted first v2 production nginx serving boundary. Live deployment is intentionally unchanged; certificate provisioning, host-specific deployment/update mechanics, process/file limits, trusted-proxy/client-IP policy, abuse/rate limiting, PostgreSQL tuning, backups/recovery and broader production observability remain separate M7 work.

## Single best next task

Resolve the **exact-candidate CI gate** for native resource-limits candidate `47f1a1b883898a33760a29117035fc7cd1897cd4` (run `37732772249`, job `113165689879`). Inspect failures and fix only the relevant implementation/fixture defects if needed, with a new exact-candidate green run before any independent local-agent execution gate. Do not integrate or deploy yet.
