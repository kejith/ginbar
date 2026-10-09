# Ginbar v2 state / handoff

Last updated: 2026-10-09
Phase: **M7 production hardening in progress; deployment/update-and-rollback dry-run CLOSED (accepted, integrated, post-integration CI green)**
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
  - first disposable deployment/update-and-rollback dry-run: **accepted, integrated, exact-candidate/post-integration CI green; mock-only with no production deployment**.
  - nginx authentication-ingress rate limiter: **accepted, integrated, exact-candidate/post-integration CI green; live target-host nginx unchanged**.
  - first trusted reverse-proxy scheme-header boundary: **ACCEPTED and integrated, nginx→real-Go/PostgreSQL and nonloopback direct-peer independently gated, exact-candidate/post-integration CI green; no production deployment**.
  - first native process/file resource-limits boundary: **accepted, integrated, independently real-binary validated and exact-candidate/post-integration CI green; live deployment unchanged**.
  - bounded password-KDF admission control: **accepted, target-host profiled and integrated; exact-candidate/post-integration CI green**.
  - first v2 production nginx serving boundary: **accepted and integrated; exact-candidate/post-integration CI green; live target-host nginx unchanged**.

## M7 production hardening — trusted reverse-proxy scheme-header boundary ACCEPTED and integrated

**Status: ACCEPTED, integrated into `v2`, and post-integration CI green.** No production host, production services or legacy `master` were changed.

Verified integration base: `e557e0284f761c96d9d4a22de267c30f4a4b47b1` (documentation/state-only). Accepted executable/configuration beneath it: `b67a4c7cfd90e6849fa4817a2e73bb6c54dcbe94`. Exact **accepted and integrated executable/configuration SHA**: `a142272cde7a74d40a128cdd9803c903fc86ef02` (fast-forwarded from verified live `v2` base with 8 commits ahead/0 behind). Feature-branch documentation-only commits `cb10b80859c59fb5e38463b2b183037f2dc278e2`, `f5fe93748764761535cedf18e5c63f9c6cc57c66`, and `7810f36af8be511a2b1e390f67b092afc729d606` were NOT integrated; this STATE commit is documentation-only above the accepted executable.

**Boundary and decisions**

- The existing nginx v2 upstream is TCP `127.0.0.1:8080`, and all three nginx API proxy locations already overwrite untrusted inbound `X-Forwarded-Proto` using `$scheme`, preserving `Host $http_host` including non-default ports. Nginx production config itself remains unchanged.
- In Go `httpapi.Server`, `X-Forwarded-Proto` is now ignored by default. Only `Config.TrustLoopbackProxy=true` permits header use, and only when the immediate TCP peer's `r.RemoteAddr` parses as loopback IPv4/IPv6 (including unmapped IPv4). Exactly one canonical `http` or `https` header value is accepted; duplicate, comma-separated, empty, whitespace-padded, uppercase or malformed header values from trusted peers fail closed for origin-mutation matching. Untrusted peers fall back to actual connection TLS/HTTP scheme regardless of forged header.
- Runtime `GINBAR_TRUST_LOOPBACK_PROXY` is explicitly parsed as boolean, **off by default**; the native systemd API unit enables it and binds `127.0.0.1:8080`. `systemd/v2/README.md` states the local-peers trust assumption and forbids enabling this setting for an externally reachable API listener. All authentication and other mutation-origin sites now call the same `s.sameOrigin` policy, instead of a global helper.
- Targeted tests in `httpapi/trusted_proxy_test.go`: 19 successful table subtests covering trusted loopback IPv4, IPv6 and mapped IPv4, explicit Host port, direct/untrusted peers, opt-in disabled, direct TLS fallback, cross-origin/port mismatch, missing or malformed forwarding values and duplicate/chained fields. `cmd/api/main_test.go` covers default off, true/false, and invalid env input. The nginx fixture now sends malicious chained and duplicate client `X-Forwarded-Proto` and verifies nginx sanitization plus explicit Host-port preservation.
- CI scope correctness fix: feature branches now compare against `origin/v2`, not only the previous feature-branch commit; nginx+backend combined deltas require the nginx gate. This ensures the executable candidate receives cumulative full gate coverage.

**Exact-candidate CI:** `v2 CI` run `37739200559`, correctness job `113185797164`, branch `astra/m7-trusted-proxy-scheme`, exact `head_sha=a142272cde7a74d40a128cdd9803c903fc86ef02`, **SUCCESS**. Runner performed full `scope=all` validation relative to `origin/v2`: Go formatting/vet/PostgreSQL suite; all 19 proxy cases; Rust fmt/check/PostgreSQL tests/clippy; frontend validation (48 passing); isolated systemd limits fixture; real nginx TLS/headers/upload-streaming fixture; and exact target Rust worker release binary build. Checkout remained tracked-clean. Log markers included `v2-service-limits-test: PASS fds=4096/1024 tasks=256/128 memory=1G/4G restart=verified`, `v2-nginx-test: PASS`, and `v2-ci: PASS sha=a142272cde7a74d40a128cdd9803c903fc86ef02 scope=all`. Superseded pre-candidate runs failed Go formatting or uncovered old `sameOrigin` callsites; those were corrected in the exact green candidate. Some subsequent runs were cancelled by branch concurrency; they were not relied on for acceptance.

**Independent uploaded gate review, 2026-10-08:** Exact transferred ZIP `trusted-proxy-20261008T092003Z.zip`, verified SHA-256 `04f29d4174d84a5ccec1017e25036fa748322d189971a559e51a0563548d9d95`; ZIP integrity valid, raw probe scripts/HTTP summaries/Go and nginx logs, migration and cleanup evidence inspected. Exact executable SHA `a142272cde7a74d40a128cdd9803c903fc86ef02`, detached tracked-clean. The real nginx TLS → Go candidate + disposable PostgreSQL gate verified nginx sanitization **7/7**, authenticated registration/login/mutation **5/5**, trust-off controls, local Go unit **19/19**, and cleanup (temporary database and worktree removed; original checkout unchanged). Direct-loopback trust-on probes **10/11** matched initial expectations: HTTP header parsing strips wire optional leading whitespace from `X-Forwarded-Proto:  https`, yielding a canonical `https` and 204; Go's in-process test still rejects a manually noncanonical header. This is a test expectation mismatch, not a demonstrated origin-policy bypass.

**Supplemental nonloopback direct-peer gate, accepted 2026-10-08:** Uploaded `nonloopback-direct-peer-20261008T101533Z.zip` verified integrity and exact transferred SHA-256 `f8a9ea4759a855e99da0f1520cea5bbe2d98cd1ccdd0b81435604c41c10ec975`; inspected `findings.md`, scripts, byte-exact raw HTTP transcripts, socket `ss` evidence, API process/logs, migration evidence and cleanup. Tested detached-clean executable `a142272cde7a74d40a128cdd9803c903fc86ef02` with `GINBAR_TRUST_LOOPBACK_PROXY=true` on a test-only address `10.255.255.254:18281` (host-local `lo` nonloopback alias; no outside traffic). Server-side `ss` shows the API's actual peer `10.255.255.254:45259`. Nonloopback forged `X-Forwarded-Proto: https` with HTTPS Origin correctly returned **403 `origin_not_allowed`**; nonloopback direct HTTP Origin without spoof returned **204**; trusted loopback 127.0.0.1 with forwarded HTTPS returned **204** on the same trust-enabled API; unknown-user login using disposable migrated PostgreSQL returned **401 `invalid_credentials`**. Temporary API stopped/port closed, disposable database dropped, worktree removed/clean canonical checkout; previous evidence HTTP transfer server PID `70543` / port `18181` was stopped and verified closed. This completes the final direct-peer security criterion without a code change. Environment used a locally scoped nonloopback IP alias because Docker was unavailable; this still exercises the real `netip.Addr.Unmap().IsLoopback()` branch.

**Post-integration CI:** `v2 CI` run `37770386248`, job `113288263459`, branch `v2`, exact executable SHA `a142272cde7a74d40a128cdd9803c903fc86ef02`, **SUCCESS**; `scope=all` over base `e557e0284f761c96d9d4a22de267c30f4a4b47b1`. Go formatting/vet/PostgreSQL tests (including 19 proxy cases), Rust correctness, frontend (48 passing), nginx TLS fixture, systemd limit fixture, target Rust release build and tracked-clean verification all passed. Log markers: `v2-service-limits-test: PASS`, `v2-nginx-test: PASS`, `v2-ci: PASS sha=a142272cde7a74d40a128cdd9803c903fc86ef02 scope=all`. Candidate CI remains run `37739200559`/job `113185797164` green. Accepted/executable integration was fast-forward-only, with no production deployment.

**Residual risk and boundary:** Both live nginx TLS → Go API → PostgreSQL and separate nonloopback direct-peer proof are accepted; no known proxy-header/origin correctness blocker remains. Go trusts all immediate local loopback peers when explicitly enabled, not exclusively nginx; keep the API listener loopback-only and treat host-local processes as trusted. HTTP parsers normalize wire optional whitespace: `X-Forwarded-Proto:  https` may appear canonical to Go; prior raw header behavior is a test expectation difference, not a demonstrated bypass. The latest supplemental evidence-transfer web server (reported PID `71912`, port `18182`) was still left running after the local-agent upload and should be explicitly stopped after confirming process identity; this is evidence transport hygiene, not an application defect. Separate M7 work remains: abuse controls, DB tuning, backups/recovery, observability and deployment. M7 remains **open**.

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

## M7 production hardening — native API/worker resource-limits boundary accepted and integrated

Verified original integration base: `544a4412141eacf6148daf4c74a1553fb9ab6b22` (documentation-only); preceding accepted executable/configuration SHA: `20be4765e935e715f2cd8862da0a6382e1322281`.

Implementation branch: `astra/m7-service-limits`.
**Exact accepted and integrated executable/configuration SHA: `b67a4c7cfd90e6849fa4817a2e73bb6c54dcbe94`.** The feature branch subsequently received documentation/state-only commits; those were **not** integrated.

- API native systemd unit `systemd/v2/ginbar-api-v2.service`: `LimitNOFILE=4096`, `TasksMax=256`, `MemoryMax=1G`, `TimeoutStopSec=30s`.
- Rust worker native systemd unit `systemd/v2/ginbar-worker-v2.service`: `LimitNOFILE=1024`, `TasksMax=128`, `MemoryMax=4G`, `TimeoutStopSec=90s`.
- Both run as `ginbar` with private env files, `KillMode=control-group`, `OOMPolicy=kill`, `MemoryAccounting=yes`, `Restart=on-failure`, `RestartSec=10s`, and 5 starts per 5 minutes. Worker memory/tasks include spawned FFmpeg/FFprobe child processes.
- Paths and prerequisites are documented under `systemd/v2/README.md`; no tracked v2 deployment automation was introduced; the legacy Docker Compose `wallium.service` is untouched.
- `scripts/v2-service-limits-test.sh` validates unit syntax, actual process/cgroup limits and restart under disposable Docker systemd on isolated CI. The CI path filter and `scope=all` correctness gate include the new units and fixture.

Exact-candidate `v2 CI`: run `37733659202`, correctness job `113168249765`, `head_sha=b67a4c7cfd90e6849fa4817a2e73bb6c54dcbe94`, **success**, `scope=all`; including Rust/PostgreSQL tests/clippy, Go/PostgreSQL tests/vet/format, frontend 48/48/typecheck/build, nginx fixture and isolated systemd fixture (`v2-service-limits-test: PASS fds=4096/1024 tasks=256/128 memory=1G/4G restart=verified`). Earlier candidate CI failures were due to a disposable Docker cgroup bind-mount defect; the final systemd fixture corrected it before acceptance.

Independent uploaded local-agent evidence archive `resource-limits-20261008T061043Z.zip`, independently hashed on receipt: SHA-256 `a2f9cfac0429e6c5a4fc90b467a6d3efdef6864d76fd7cd4155aacdea5027a08`; ZIP integrity verified and raw evidence inspected. The archive's in-file hash `9f72152ffccccab72fe9e814dbf40e8657d3e4441b05f1443add295f8393c0ed` was explicitly marked provisional before repacking, not the transferred archive hash.

Independent real-executable evidence on Ubuntu 24.04 WSL2, systemd 255, PostgreSQL 16.15, Go 1.27.1, and Rust release:
- detached exact-candidate worktree was tracked-clean; Go API and worker binaries built from the exact SHA;
- user-scope transient systemd services applied exact FD/task/memory/restart/stop/OOM/kill policies; verified through `systemctl show`, `/proc/<PID>/limits`, and cgroup `pids.max`/`memory.max`;
- API `/healthz` 200, unauthenticated `auth/me` 401, invitation-backed registration 201, login 200 and authenticated session read passed;
- upload 201 created post/job 1; worker `run` processed PNG to AVIF and thumbnail, job succeeded in one attempt, release published, and media status reported ready;
- cgroup peaks under this small test: API `140963840` bytes (~134.4 MiB), 12 tasks, 7 open FDs; worker `17440768` bytes (~16.6 MiB), 23 tasks, 7 open FDs;
- graceful stops left both units inactive and freed the API port; after forced API `SIGKILL`, systemd restarted it after ~10 seconds with the original limits intact;
- disposable DB dropped and confirmed absent; PG stopped; all test units/worktree/media/binaries removed; canonical checkout's pre-existing tracked/untracked changes remained unchanged. No live host/deployment/production-data modification.

**Acceptance:** the first native process/file resource-containment boundary is **accepted and integrated**. The independent WSL test used systemd **user** transient units with equivalent numeric settings, not installed system-scope `User=ginbar` units; the separate exact-candidate CI verified actual system-scope unit syntax/limit effects with stubs. The fixture is one small PNG and does not establish worst-case 4 GiB video/FFmpeg or 1 GiB API memory capacity. Host-specific unit installation and production-load calibration remain separate M7 work.

Integration was a non-force fast-forward of live `v2` from `544a4412141eacf6148daf4c74a1553fb9ab6b22` to exact candidate `b67a4c7cfd90e6849fa4817a2e73bb6c54dcbe94`, with ahead=10, behind=0 and expected-SHA lease.
Post-integration `v2 CI`: run `37737804515`, correctness job `113181252646`, branch `v2`, exact `head_sha=b67a4c7cfd90e6849fa4817a2e73bb6c54dcbe94`, **success**. Full `scope=all` correctness, service fixture, real nginx fixture, Rust worker release-build applicability/execution and tracked-clean checkout all passed. No CI/correctness blocker remains from this slice.

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

No unresolved correctness, security-boundary, cache-policy, range, source-exposure, proxy-header, upload-streaming or CI blocker remains from the accepted first v2 production nginx serving boundary. Live deployment is intentionally unchanged; certificate provisioning, host-specific deployment/update mechanics, trusted-proxy/client-IP policy, abuse/rate limiting, PostgreSQL tuning, backups/recovery and broader production observability remain separate M7 work.

No known resource-limit unit, applied-cgroup, real-binary startup, graceful shutdown/restart, cleanup, or CI blocker remains after the accepted native service-boundary gate. Resource caps are provisional containment rather than established production-load capacity; untested large-video peak usage, FFmpeg child-process load and installed system-scope service operation must be revalidated separately before production deployment.

## Single best next task

Verify the **post-integration** `v2 CI` branch-push run `37828358188` for exact executable/configuration SHA `5da0bbad5c21f2ee289265c128f84a1cf7a597c9`, including correctness job SHA, conclusion and all required steps. Mark this health/readiness boundary CLOSED only after independent exact integration-push GREEN is verified. Do not start another M7 slice before this gate.

## M7 native nginx authentication-ingress limiting — accepted and integrated

**Decision (2026-10-08): ACCEPT independent evidence, integrate executable; do not close the post-integration CI gate without its own success evidence.**

- Live integration base verified before update: `043dd8dba33f2adcc08e657ced6ea096d09a03a6` (STATE-only); integration performed via non-force fast-forward with expected-SHA lease.
- Accepted executable/configuration candidate and integrated exact SHA: `1e60983597f1651a738f9c095e915c7facf6e305`. After update, remote `v2` compared identical to this SHA. Branch: `astra/m7-nginx-auth-ingress-limit`; branch STATE-only commits excluded from integration.
- Config: shared 10 MiB `limit_req_zone $binary_remote_addr zone=ginbar_v2_auth:10m rate=2r/s`, `burst=10 nodelay`, HTTP 429 for two exact auth route locations. TCP-peer-derived key ignores spoofed XFF. Shared-NAT peers share budget. Other API locations, TLS, explicit Host/forwarded proto and upload/URL streaming directives left intact.
- Exact candidate `v2 CI`: workflow id `374214168`, run `37791902843`, correctness job `113361650640`, head SHA `1e60983597f1651a738f9c095e915c7facf6e305`, **completed/success**, verified in local acceptance archive via recorded GitHub API run/job/check-run JSON. Job logs unavailable due token restrictions; CI verdict is based on recorded API conclusions.
- Independently supplied acceptance ZIP `nginx-auth-accept-20261008T145859Z.zip` integrity-verified and SHA-256 `8d9fa93f1d87e0c8131db618566a8da827228f62f80e634f47e39d4ca24cfd79`; inspected findings, raw nginx test results and CI records. Exact-SHA detached-clean real nginx 1.24.0 acceptance PASS: bash syntax and isolated `nginx -t`, Host/scheme/header sanitation, login/register +2 upstream, shared burst 10/10, forged-XFF flood 34×429/1×200 with only +1 upstream, shared exhausted registration 429, non-auth route 200, recovery without restart. Isolated fixture/probe was Docker-free; exact Docker fixture coverage is via green candidate CI. No acceptance blocker found.
- **Post-integration `v2 CI`: not yet verified.** Available GitHub connector does not provide a general workflow run listing/dispatch and public Actions URL could not be fetched here. Do not claim post-integration success or final CI closure until workflow/run/job/head SHA/result are inspected. This is an evidence gate, not a demonstrated defect.
- Remaining risks: shared NAT policy may need real-load tuning; target-host evidence transfer server 18182 cannot be safely terminated from this environment without PID/command verification. No production deployment or legacy `master` modification occurred.

**Exactly ONE next task:** Verify the post-integration `v2 CI` workflow/run/job for exact executable SHA `1e60983597f1651a738f9c095e915c7facf6e305` and record GREEN evidence in STATE before proceeding to any next M7 implementation.


### Post-integration CI verification retry — 2026-10-08

- Reverified remote `v2` is documentation-only `7344dc83b62e5bffb36fd9e1a1038cf57b7e2f85`, with integrated executable/configuration SHA `1e60983597f1651a738f9c095e915c7facf6e305` recorded above.
- Rechecked candidate SHA through available GitHub connector: commit-associated workflow-run listing returned `[]` (this integration only covers pull-request-triggered runs) and combined commit statuses returned `[]` (not an authoritative Actions check-run listing). Direct GitHub Actions and API web retrieval failed. Therefore post-integration workflow/run/job/head SHA/conclusion **remain unverified**, neither PASS nor FAIL. Do not conflate feature-branch candidate CI run `37791902843` with a `v2` post-integration run.
- No executable, CI configuration or production host/service changes were made; accepted integration remains in place. No process was signaled at port `18182` (no target-host PID/command identity available).
- **Exactly ONE next task:** On a network-capable authorized host with Actions API access, locate the `v2`-branch `v2 CI` push run for `1e60983597f1651a738f9c095e915c7facf6e305`; verify its correctness job and conclusion, then record exact run/job/SHA and close the slice only on success. If absent or failed, diagnose the CI gate before any next M7 implementation.


### Post-integration CI verified GREEN — 2026-10-08

- Verified from a network-capable host via the public GitHub Actions API against workflow `v2 CI` (`374214168`, `.github/workflows/v2-ci.yml`).
- All 33 `v2`-branch push runs enumerated; exactly one run has `head_sha=1e60983597f1651a738f9c095e915c7facf6e305`, and it is not the feature-branch candidate run `37791902843`.
- Post-integration `v2 CI`: run `37801213689`, branch `v2`, event `push`, exact `head_sha=1e60983597f1651a738f9c095e915c7facf6e305`, status `completed`, conclusion `success` (created 2026-10-08T15:29:41Z).
- Correctness job `113393626580` (`correctness`), identical `head_sha`, status `completed`, conclusion `success`; all 8 recorded steps success (setup, exact-revision checkout, checkout verification, scoped v2 correctness gate, target worker release build, tracked-clean verification, post-checkout, completion).
- Documentation-only pushes `7344dc83b62e5bffb36fd9e1a1038cf57b7e2f85` and `b481c03f03be6652c00d6b21f383599808f66a1d` have no `v2 CI` runs in the full 33-run listing because `docs/**` matches no entry in the workflow `paths` filter (verified in `.github/workflows/v2-ci.yml`); the executable-SHA run above is the applicable gate and it is green.
- Live remote `v2` verified as documentation-only `b481c03f03be6652c00d6b21f383599808f66a1d` with integrated executable `1e60983597f1651a738f9c095e915c7facf6e305` (ancestor, zero divergence). No executable, CI configuration, production host/service, or legacy `master` change was made by this verification. No local process listens on port `18182`; nothing was signaled (target-host evidence-server identity still unverified from here).
- Raw API JSON (workflows, both run-listing pages, run, jobs, branch) retained in evidence ZIP `post-integration-ci-20261008T160515Z.zip`.

**Decision: the nginx authentication-ingress slice is CLOSED.** Exact-candidate CI, independent nginx acceptance, and post-integration CI are all green with no known correctness, security-boundary, burst/recovery, spoof-immunity, or CI blocker remaining. Shared-NAT budget tuning stays a real-load calibration item, not a slice blocker. M7 production hardening remains in progress.


## M7 PostgreSQL logical backup/restore primitive — candidate awaiting CI

**Status (2026-10-08): implementation committed to feature branch, NOT CI-green verified, NOT accepted or integrated.**

- Integration base: verified remote `v2` `8732c12b0e8998d12df5b8d0ff26661847790359` (STATE-only); accepted executable beneath `1e60983597f1651a738f9c095e915c7facf6e305`.
- Branch `astra/m7-pg-backup-restore` started from the verified live base. Executable/CI candidate before this STATE-only update: `501ddefffb637d73a62d5ec6bc4e5f049ec9ecc6`.
- New `scripts/v2-pg-backup-test.sh`: isolated disposable PostgreSQL 17.11 container; creates separate source/restore databases; migrates SQL files from the v2 schema; seeds FK-linked users/credentials/posts/tags/comments/votes plus nondefault identity values; `pg_dump -Fc` and `pg_restore --exit-on-error --no-owner --no-privileges`; deterministic sorted JSONB row fingerprints and counts for all public tables, sequence catalog comparison, schema-only dump comparison, and transactional identity/FK usability probe. Prints versions, SHA-256, size and elapsed time. Container and temporary files removed via EXIT cleanup. Does not access production connections or data.
- `scripts/v2-ci.sh` auto-scope includes the fixture path and invokes its gate in the nginx/all CI path; `.github/workflows/v2-ci.yml` push paths include the fixture. This is intentionally full-scope CI.
- **Validation evidence: NOT YET RUN** on a Docker-capable execution runner; no reproducible PASS claim, exact-candidate workflow/run/job/SHA/result or fixture timing is available. Potential fixture/schema incompatibilities must be fixed before acceptance. A green CI verdict on the exact executable SHA is mandatory before LOCAL review.
- No changes to legacy `master`, persistent production state, services, deployment, WAL/PITR, retention, or automation. No integration performed.

**Exactly ONE next task:** Run/fix the disposable PostgreSQL backup/restore fixture and exact-SHA `v2 CI` to GREEN, document precise results, then request independent LOCAL EXECUTION AGENT acceptance. Do not integrate until accepted.

### Backup fixture candidate correction — 2026-10-08

- Exact latest executable/CI candidate: `37f801861bd90aa2327b0b9c448c6b684e018b8d` (supersedes pre-correction `501ddefffb637d73a62d5ec6bc4e5f049ec9ecc6`). Schema DDL comparison normalizes volatile pg_dump database headings and restrict tokens before byte comparison.
- **Exact-SHA CI and disposable PostgreSQL execution remain NOT VERIFIED**; no test timing, workflow run/job/result or green conclusion is asserted. Do not pass to independent acceptance or integrate without green exact-SHA CI.
- **ONE next task:** Execute and repair the fixture on an authorized Docker CI runner and obtain verified successful `v2 CI` for the exact executable SHA, updating STATE with its workflow/run/job/SHA/result before independent local acceptance.


## M7 PostgreSQL disposable logical backup/restore — CLOSED (accepted, integrated, post-integration CI green)

**Gate state: CLOSED after verified post-integration CI.** Live `v2` base before integration: `8732c12b0e8998d12df5b8d0ff26661847790359` (documentation-only). Non-force expected-SHA fast-forward to exact accepted executable/configuration `6eb1721ba280278f022bf11c13c1fd698b565b3b` succeeded, and remote `v2` was independently verified identical after update. Feature branch `astra/m7-pg-backup-restore`; its STATE-only commits were not integrated.

- Added `scripts/v2-pg-backup-test.sh`: disposable PostgreSQL source/restore, sorted migrations, FK-linked fixture, native `pg_dump -Fc` / `pg_restore --exit-on-error`, deterministic public-table data fingerprints, sequences, native pg_restore TOC object inventory comparison, and rolled-back restored usability. Added `scripts/v2-ci.sh` and `.github/workflows/v2-ci.yml` coverage.
- Exact-candidate `v2 CI`: run `37821288937`, correctness job `113462683224`, event push, branch `astra/m7-pg-backup-restore`, exact SHA `6eb1721ba280278f022bf11c13c1fd698b565b3b`, completed/**success** (8 successful job steps per raw archived API result).
- Separate LOCAL INDEPENDENT ACCEPTANCE ZIP `pg-backup-acceptance-20261008T182246Z.zip`: independently verified ZIP integrity, SHA-256 `8c7fee043488f6dd78f5f181c3c7ef7c862b8a13dd3e27e8158ef8a5533c83e7`. Inspected raw findings/test transcript, CI records and cleanup. Fresh detached-clean worktree on exact SHA. PostgreSQL 16.15 disposable adapter (Docker daemon unavailable locally): 9/9 migrations; `pg_dump -Fc` 102375 bytes / ~0.05s; strict restore passed; 17 tables + 10 sequences identical, 100/100 schema TOC entries identical, restored identity/FK passed, restored CHECK/FK invalid rows rejected, truncated backup restore and tampered-snapshot comparison both failed closed, source before/after identical. Database, worktree and resources cleaned; no production write. Exact Docker fixture not run by LOCAL; applicable candidate CI concluded success.
- TOC inventory equality verifies schema-object inventory rather than SQL expression byte-equivalence. Strict restore and constraint enforceability checks supplement it. No known acceptance blocker.
- No production deployment, schedule/automation, WAL/PITR, retention, off-host backup storage, DB tuning or legacy `master` changes.
- **Post-integration `v2 CI` for the exact executable SHA: pending verification; no run/job/result is claimed here.** Do not mark CLOSED based on feature-branch candidate CI.

**Exactly ONE next task:** Verify the post-integration `v2 CI` push workflow/run/job, exact head SHA `6eb1721ba280278f022bf11c13c1fd698b565b3b`, and conclusion through an authorized Actions API host; update STATE and close this slice only on GREEN. Do not begin another M7 slice first.

### Final post-integration verification and closure — 2026-10-08

- Received independent read-only CI evidence ZIP `pg-backup-postci-20261008T183443Z.zip`, verified ZIP integrity and SHA-256 `0f059eea7558f5a698900501e73095972790128674b264abe2c55b7d98236b84`. Reviewed raw GitHub Actions JSON and findings. Live `v2` prior to this STATE-only update was `9d32d6fec80a673c935dc509ae67c1c11a7ce1be`, executable remains `6eb1721ba280278f022bf11c13c1fd698b565b3b`.
- **Post-integration `v2 CI`: SUCCESS.** Workflow `v2 CI` ID `374214168`, run `37824673076`, event `push`, branch `v2`, exact head SHA `6eb1721ba280278f022bf11c13c1fd698b565b3b`, status `completed`, conclusion `success`. Raw `ci-jobs-37824673076.json` establishes correctness job **`113474314789`**, exact SHA, completed/success and 8/8 successful recorded steps (including scoped correctness, worker release-build applicability and tracked-clean verification).
- **Evidence correction:** the local handoff summary and archived findings incorrectly repeated candidate correctness job `113462683224` for the post-integration run. This is **not** the post-integration job ID. The actual post-integration job is `113474314789` as recorded in raw job API JSON. Candidate CI remains run `37821288937` / job `113462683224` and is not substituted for post-integration verification.
- Integration-triggered CI scope `all` was applicable because the pushed executable range changes the workflow, CI script, and backup fixture paths. The feature implementation was a fast-forward, no merge or production deployment. Separate doc-only STATE updates are not executable CI triggers.
- All mandatory gates are now complete: IMPLEMENTED → diagnostic fixture PASS → EXACT_CANDIDATE_CI_GREEN → INDEPENDENT_EVIDENCE_PASS → CODING_ACCEPTED → INTEGRATED → POST_INTEGRATION_CI_GREEN → **CLOSED**. No known backup/restore primitive correctness or CI blocker remains. This establishes a disposable logical-backup/restore verification procedure, not scheduled/off-host backup durability or PITR. M7 remains open.

**Exactly ONE next task:** Implement a narrowly scoped v2 API/worker operational health and readiness observability boundary, starting from freshly verified live `v2`: establish low-cost health/readiness signals and deterministic isolated tests without new deployment automation, production host changes, metrics storage, or alerting infrastructure. Obtain exact-SHA green CI and independent evidence before integration.


## M7 API/worker health-readiness observability — CLOSED (accepted, integrated, post-integration CI green)

**Decision 2026-10-08: ACCEPT independent evidence and integrate exact executable. Do not mark CLOSED until verified post-integration push CI GREEN.**

- Verified integration base before fast-forward: `2550fac27c9d7fd1dbf7c96c85f5daaf7d68dce9` (documentation-only), prior accepted executable `6eb1721ba280278f022bf11c13c1fd698b565b3b`.
- Feature branch `astra/m7-health-readiness`; exact accepted executable/configuration SHA `5da0bbad5c21f2ee289265c128f84a1cf7a597c9`. Feature branch STATE-only HEAD `f70fa41fa25f7913244fcd8eb9f300281c78cf5f` explicitly excluded from integration.
- API `GET /healthz` remains liveness only (200 `{"status":"ok"}`), independent of PostgreSQL. Added `GET /readyz` (bounded one-second PostgreSQL ping, one in-flight DB probe per API process, 200 `{"status":"ready"}` or generic 503 `{"status":"not_ready"}`, no-store). Worker adds a separate `ready` one-shot bounded PostgreSQL `SELECT 1` command with generic failure, no claims or media-root requirement. Systemd stays the worker process-liveness authority.
- Exact-candidate `v2 CI`: workflow ID `374214168`, run `37826767139`, correctness job `113481508806`, exact candidate SHA, feature-branch push, completed/success, full scope=all and all eight recorded job steps successful (including worker release build and tracked-clean check).
- Independently uploaded evidence ZIP `m7-health-readiness-20261008T185353Z.zip`, verified integrity and SHA-256 of exact uploaded bytes `e3fd990358f635fb8bb85b1737dbc79b04f6e29970593ec3dd7ac0e8c6ebbaae`. Inspected `findings.md`, raw API HTTP transcripts, concurrency summary, CI JSON, build logs, worker stdout/stderr/exit timing, durable before/after state, provenance and cleanup. Exact detached-clean candidate SHA. Evidence PASS: API health stayed 200 while readiness changed 200→503→200 on disposable DB loss/recovery in same API process; readiness generic/no leak; 50/50 liveness and 42/50 readiness 200 with 8/50 fail-closed 503 under concurrent pressure, observed peak DB connection 1 and FDs 8→8, threads 10→10; worker healthy/unavailable/recovered `ready` exited 0/1/0 with generic failure and no mutation, exact same binary on recovery; disposable database/worktree/media/services cleaned.
- Limitations: local Docker unavailable, authorized native disposable PostgreSQL fallback used; peak DB connections sampled at 5 ms intervals and may under-sample. Combined evidence and CI sufficient for this narrow boundary, not a general performance/availability assurance.
- **CODING_ACCEPTED → INTEGRATED:** non-force expected-SHA fast-forward moved `v2` from `2550fac27c9d7fd1dbf7c96c85f5daaf7d68dce9` to `5da0bbad5c21f2ee289265c128f84a1cf7a597c9`, and re-read remote `v2` confirms exact SHA, zero divergence.
- **Post-integration CI:** workflow `v2 CI` ID `374214168`, run `37828358188`, branch `v2`, event `push`, exact head SHA `5da0bbad5c21f2ee289265c128f84a1cf7a597c9`, observed `queued`; correctness job/result not yet verified. The feature-branch candidate run cannot substitute for it.
- No deployment, persistent production change, live systemd/nginx change, backup/PITR or `master` modification.

### Final post-integration verification and closure — 2026-10-08

- Received independent LOCAL read-only post-integration CI evidence ZIP `post-integration-ci-20261008T190005Z.zip`. Verified ZIP integrity (no corrupt entries); SHA-256 of the exact uploaded ZIP bytes `3206f2311ffa3f2b1bba0ece466e6f4084b0120e50a033aac56a030736ceb3bd`. All 21 entries listed in `MANIFEST.sha256` match, with no missing or additional evidence entries; inspected `findings.md`, initial/poll/final raw workflow/job JSON, provenance, versions and cleanup proof. Sidecar ZIP checksum from the local agent was not supplied here, so the verified SHA above is independently calculated from uploaded bytes.
- Independently re-read authenticated GitHub Actions API: workflow `v2 CI` ID `374214168`, integration push run `37828358188` (run number 252), event `push`, branch `v2`, exact executable SHA `5da0bbad5c21f2ee289265c128f84a1cf7a597c9`, status `completed`, conclusion `success`, updated `2026-10-08T19:02:36Z`; correctness job `113486954645` matches run/SHA/branch and is `completed/success`. All **8/8** job steps completed/success: setup, checkout exact revision, verify checkout, scoped correctness, target worker release build, verify tracked checkout unchanged, postcheckout and complete.
- Raw workflow logs were inaccessible to the independent agent (public GitHub API HTTP 403); raw Actions run/job/step JSON is sufficient for the exact-SHA CI completion/conclusion gate and is cross-verified against live GitHub API. The exact-candidate feature run `37826767139` is separate and was not substituted.
- Integration base and final HEAD relation re-verified: `5da0bbad5c21f2ee289265c128f84a1cf7a597c9` is the direct ancestor of previously live docs-only `9d86def3d1c400ce8bf109f8a6bfd3c271fe1a15`; `compare_commits` shows the only following file change is `docs/v2/STATE.md`. Closing this gate changes only documentation; no executable/configuration changes or new CI claims.
- **Final gate transitions:** IMPLEMENTED → LOCAL_FIXTURE_PASS → EXACT_CANDIDATE_CI_GREEN → INDEPENDENT_EVIDENCE_PASS → CODING_ACCEPTED → INTEGRATED → POST_INTEGRATION_CI_GREEN → **CLOSED**. The API/worker readiness boundary is closed; overarching M7 is still in progress. No production deployment, monitoring service, or alerting stack is implied.

**Exactly ONE next task:** Define and implement the first narrowly scoped **v2 deployment/update-and-rollback dry-run procedure** using disposable local services only, beginning with live `v2` and the deployment units already in-repo. Exclude production deployment and host writes. Obtain exact executable/configuration SHA green CI and independent LOCAL evidence before integration.

## M7 disposable deployment/update-and-rollback dry-run — CODING ACCEPTED and INTEGRATED; post-integration CI pending

**Status (2026-10-08):** Exact implementation accepted after independent execution evidence, and guarded fast-forward integrated into `v2`. **NOT CLOSED** until independent post-integration push CI verification succeeds. M7 overall remains open.

- Verified integration base: `3f0bfcf331606d981492597fc6e602c428b105ab` (documentation-only). Prior accepted executable: `5da0bbad5c21f2ee289265c128f84a1cf7a597c9`. Feature branch `astra/m7-deploy-rollback-dry-run`. Exact accepted and integrated executable/configuration SHA: `ac92e9e91d6ccdbb19982c929f8d66832d515a0b`; compare to base = 5 ahead, 0 behind, fast-forward non-force expected-SHA. Feature docs-only HEAD `77136ee8776a21eba9bc7607ee79c7a21974add1` was NOT integrated; compare executable → docs-only shows only `docs/v2/STATE.md`. Remote `v2` re-read after ref update confirms exact executable SHA.
- Implemented `scripts/v2-deploy-dry-run-test.sh`: disposable privileged Docker systemd + nginx boundary with original v2 API/worker unit and nginx configs, isolated mock releases A/B/bad, per-release API/worker/frontend symlinks and container-only locked atomic release switching. Checks A→B update; bad readiness fails closed and reactivates B; unsafe `../a` and missing releases rejected; explicit B→A rollback. Each stage checks unit activity, direct API readiness, proxied TLS scheme/Host release, frontend, processed-media stability and ingestion-source denial. EXIT trap removes disposable container/image/build context. Workflow path filters and `scripts/v2-ci.sh` full-scope gate updated; systemd README documents test, boundaries and non-production use. No real Go/Rust migration, live deployer, zero-downtime claim or persistent host/config/DB write.
- Exact-candidate CI verified: workflow `v2 CI` ID `374214168`, feature-branch `push` run `37830237355`, correctness job `113493599564`, exact head SHA `ac92e9e91d6ccdbb19982c929f8d66832d515a0b`, completed/success and 8/8 recorded job steps success. Raw runner log includes `v2-deploy-dry-run-test: PASS initial=a update=b failed=bad recovered=b rollback=a nginx=verified media=stable`, failed-bad rollback/recovery markers, PostgreSQL backup PASS, nginx PASS, service limits PASS and `v2-ci: PASS sha=ac92e9e91d6ccdbb19982c929f8d66832d515a0b scope=all`.
- Prior LOCAL attempts `deploy-rollback-dryrun-20261008T192039Z.zip` and `deploy-rollback-isolated-20261008T192635Z.zip` were BLOCKED (missing Docker and isolated CI VM access, respectively); neither conferred acceptance.
- **Independent execution acceptance evidence:** uploaded `deploy-rollback-amp-20261008T193826Z.zip`; independently computed SHA-256 of uploaded bytes **`1cc7b609777145e7d99edab90a2dafb26e352d8819461cf4d62d16b762fa8416`**. ZIP test PASS; all 18 `MANIFEST.sha256` entries verified and no extra evidence files. Inspected raw fixture stdout, exit status 0, failed candidate/unsafe/missing rejection, rollback to B and explicit rollback A, remote-local SHA-256 of four consumed fixture files byte-identical, exact candidate SHA, remote setup/teardown, CI run/job JSON, versions, findings and cleanup. Previous explicit operator authorization overrode the production-host exclusion for this isolated fixture **on `root@amp.kejith.de` only**. Ubuntu 24.04.3, Docker 28.5.1, cgroup v2; pre-existing production containers unchanged by observations, temporary fixture container/image and extracted tree removed. Original local checkout remained tracked-clean; no production deploy/DB writes, master changes or persisted host config changes are claimed. Evidence is a stand-in service orchestration validation and does not validate real Go/Rust deploys or workload/migration rollback.
- **CODING decision:** independent fixture evidence PASS, accepted within this limited scope. Guarded non-force expected-SHA fast-forward `v2` from `3f0bfcf331606d981492597fc6e602c428b105ab` to exact executable SHA `ac92e9e91d6ccdbb19982c929f8d66832d515a0b` succeeded; live ref reread exact. No feature docs-only commits integrated. Integration push triggered `v2 CI` workflow `374214168`, run `37834847241`, `v2`/`push`/exact SHA. First observed **queued**; job ID and conclusion not yet established. Candidate CI does **not** substitute for this post-integration gate.

### Final post-integration CI verification and closure — 2026-10-08

- **Post-integration push `v2 CI`: SUCCESS.** Workflow ID `374214168`, run `37834847241` (run number `257`), event `push`, branch `v2`, exact executable/configuration head_sha `ac92e9e91d6ccdbb19982c929f8d66832d515a0b`, status `completed`, conclusion `success`, updated `2026-10-08T19:55:48Z`. Correctness job **`113509152156`** completed/success on this run; all **8/8** job steps completed/success (setup, exact checkout, SHA verification, full-scope correctness, target worker release build, tracked-clean verification, post checkout and complete). This was verified from the live GitHub Actions run/job/step API and is distinct from exact-candidate feature run `37830237355`/job `113493599564`.
- Raw post-integration job log independently inspected: `v2-service-limits-test: PASS`, `rollback recovered: b`, `v2-deploy-dry-run-test: PASS initial=a update=b failed=bad recovered=b rollback=a nginx=verified media=stable`, `v2-pg-backup-test: PASS`, `v2-nginx-test: PASS`, `v2-ci: PASS sha=ac92e9e91d6ccdbb19982c929f8d66832d515a0b scope=all`. Worker release-build and clean-checkout verified by job steps.
- Repository-state audit: the accepted executable was fast-forwarded non-force to `v2` from live docs-only base `3f0bfcf331606d981492597fc6e602c428b105ab`; the subsequent STATE update was a **documentation-only** commit `6b58a4b46ec773e4dc093dfd52a1c340604616c1` (only `docs/v2/STATE.md` differs from integrated executable). No new executable/configuration change or feature-branch documentation merge is implied by this closing STATE update. Docs-only pushes do not substitute for or require new executable CI under the existing workflow path filters.
- **Final gate:** IMPLEMENTED → LOCAL_FIXTURE_PASS (CI fixture) → EXACT_CANDIDATE_CI_GREEN → INDEPENDENT_EVIDENCE_PASS → CODING_ACCEPTED → INTEGRATED → POST_INTEGRATION_CI_GREEN → **CLOSED**. The deployment fixture slice is complete. This is a disposable mock-based update/rollback verification, **not** a production deployment, actual Go/Rust release cutover, zero-downtime guarantee or reversible database migration. M7 remains open for PLAN scope not yet independently closed.

**Exactly ONE next task:** Starting from freshly fetched live `v2` and reviewing `docs/v2/PERFORMANCE.md` and the existing `src/backend/v2/bench/` tools, define the next narrowly scoped **M7 performance-regression gate** for representative workloads on disposable PostgreSQL; implement only a reproducible evidence-based regression fixture with explicit metrics/budgets, not speculative DB indexes, production data benchmarking, production changes or another M7 slice. Obtain exact-candidate CI and separate independent acceptance before integration.


## M7 representative API/PostgreSQL performance-regression fixture — IMPLEMENTED, candidate CI pending

**Status as of 2026-10-08:** implementation committed; no local Docker/PostgreSQL
execution was possible in the CODING environment, so neither local fixture
success nor independent acceptance is claimed. No integration or production
change. Repository `v2` base verified at
`36e27ed4f1671f7a50a3e99833ffb2fecb2c6e7f` (documentation-only),
previous accepted executable/configuration
`ac92e9e91d6ccdbb19982c929f8d66832d515a0b`.
Implementation branch `astra/m7-performance-regression-gate`;
exact executable/configuration candidate
`fa9df460c00b78175b748a08510c225b76592700`.
Any subsequent `STATE.md` edit on the feature branch is **documentation-only**
and does not change the executable candidate.

**Implementation:** `scripts/v2-perf-regression-test.sh` creates only disposable
network/container PostgreSQL 17.11 and API/Go 1.25.0 fixture resources; loads
existing `001_core.sql`, `bench/seed.sql` (100,000 synthetic posts) and
`bench/explain.sql` (five `EXPLAIN (ANALYZE, BUFFERS)` queries); builds and
runs existing Go `httpbench.go` against the real Go API with no published
ports. It measures first/cursor/tag+score feed c1 and around-post c1/c8,
2,000 measured requests with 20 warmups each. The Python standard-library
validator `bench/check_regression.py` enforces exactly 100% HTTP 200
success with zero errors, finite timings, five completed indexed/buffered
SQL plans, no sequential hot-table scan and no disk spill, and records
p95/p99/RPS. Optional external accepted budget JSON enables numeric
latency/throughput limits with provenance; no arbitrary numeric budgets
were invented or silently enforced. CI wiring: `scripts/v2-ci.sh` backend
scope, workflow path filter for new fixture. Detailed run, calibration and
isolation contract: `src/backend/v2/bench/REGRESSION.md`.

**Historical comparison limitation:** accepted M2 around-post p95 c1
2.277 ms and c8 4.871 ms were measured on the target host with 2,000
requests/cell; they are not directly comparable to CI VM results without
controlled same-fixture host/database/runtime/background-load measurements.
The candidate therefore delivers a deterministic structural/correctness
regression gate and raw timing capture; **numeric latency regression
thresholds are NOT calibrated**. Target repeated measurements and separate
explicit threshold acceptance are required before a numerical performance
gate is claimed.

**Execution availability:** GitHub repository push and Actions read possible;
fresh remote ref confirmed. Docker daemon, native psql, target-host access,
and direct Git clone/network were unavailable in the CODING runtime.
Feature-branch push triggered workflow `v2 CI` ID `374214168`,
initial exact-executable run `37837443059` (`push`, SHA
`fa9df460c00b78175b748a08510c225b76592700`); last observation:
`pending`, job and conclusion **unknown**. Superseded earlier branch runs
must not be used for exact-candidate acceptance. Any green result must be
verified from run/job/steps and logs where accessible.

**Gate:** `IMPLEMENTED` only. `LOCAL_FIXTURE_PASS`,
`EXACT_CANDIDATE_CI_GREEN`, `INDEPENDENT_EVIDENCE_PASS`,
`CODING_ACCEPTED`, `INTEGRATED`, `POST_INTEGRATION_CI_GREEN`,
and `CLOSED` remain unverified.

**Exactly ONE next task:** Resolve and verify the exact-candidate CI run for
`fa9df460c00b78175b748a08510c225b76592700`, repairing concrete
failures on this branch if necessary. Only after green CI should a separate
LOCAL INDEPENDENT ACCEPTANCE task execute the disposable fixture and
supply raw evidence. Do not integrate this slice or start another M7 slice.

### Exact-candidate CI verification and next gate — 2026-10-08

- **EXACT_CANDIDATE_CI_GREEN: verified.** Workflow `v2 CI` ID `374214168`,
  feature-branch push run **`37837443059`**, correctness job **`113518519620`**,
  exact head SHA **`fa9df460c00b78175b748a08510c225b76592700`**,
  completed/**success** (updated `2026-10-08T20:18:07Z`).
  All **8/8** job steps completed/success: setup, checkout exact revision,
  verify checkout, full scoped correctness, target worker release build,
  verify tracked checkout unchanged, postcheckout, complete.
  Raw job logs were fetched and independently inspected in the coding session.
  CI log explicitly contains `v2-ci: PASS sha=fa9df460c00b78175b748a08510c225b76592700 scope=all`.
- **CI disposable fixture: PASS.** Raw log contains
  `v2-perf-regression: PASS shape=bounded/indexed/no-spill http=all-200 budget=not-yet-calibrated`
  and `exit=0 sha=fa9df460c00b78175b748a08510c225b76592700`.
  Five `EXPLAIN (ANALYZE, BUFFERS)` statement execution times:
  first feed **0.219 ms**, old post-ID cursor **0.204 ms**,
  required tag+score **3.145 ms**, included/excluded tags+score
  **2.086 ms**, around post **0.405 ms**. First/cursor feeds return
  61 rows; hot post/media index scans and buffer evidence observed.
  No tracked edits or direct production database connections were made.
- **Observed CI VM HTTP workload measurements** (2,000 successful requests,
  zero HTTP errors per cell; p95 / p99 / RPS):
  - first feed c1: **1.151 ms / 1.353 ms / 1124.0**;
  - cursor feed c1: **1.158 ms / 1.286 ms / 1050.3**;
  - tag+score search c1: **2.586 ms / 3.434 ms / 513.3**;
  - around post c1: **2.328 ms / 2.682 ms / 540.9**;
  - around post c8: **7.307 ms / 9.055 ms / 1839.4**.
  These are single CI-environment observations, **not accepted latency
  budgets** or controlled comparisons with M2 target-host timings.
- Source code is still exact executable candidate
  `fa9df460c00b78175b748a08510c225b76592700`. Feature branch
  documents-only HEAD above it does not alter the tested code. Live `v2`
  remains `36e27ed4f1671f7a50a3e99833ffb2fecb2c6e7f`; no integration
  or production deployment has occurred.
- **Remaining gate:** `INDEPENDENT_EVIDENCE_PASS` and CODING acceptance,
  after representative target-host disposable fixture execution with
  multiple runs and raw evidence. The local CODING environment still
  lacks Docker/PostgreSQL, so no LOCAL workstation PASS is claimed.
  No timing threshold is enforced without an accepted comparable baseline.

**Exactly ONE next task:** LOCAL INDEPENDENT ACCEPTANCE of executable SHA
`fa9df460c00b78175b748a08510c225b76592700` now that exact-candidate
CI is green. Execute this fixture in a detached clean checkout with
disposable services, collect multiple comparable baseline measurements,
raw query plans, result JSON, versions, host-load snapshots, error paths,
SHA-256 ZIP manifest and cleanup proof in one evidence ZIP under
`.local-agent-results/`. LOCAL must not change tracked files, integration
refs or production services. CODING reviews raw ZIP and decides acceptance,
threshold policy and guarded integration in a separate step. No other M7 slice.

### Independent evidence ZIP review — provenance mismatch, candidate amended (2026-10-08)

**Result: BLOCKED for independent acceptance; no integration.** Uploaded
`perf-regression-20261008T202505Z.zip`; independently calculated SHA-256
`e1a4fa1c96fd7ffb125841e364759885731b85a57c20d673d35a4c3f6c81b5b9`.
ZIP CRC passes. Normalizing `./` path prefixes in `MANIFEST.sha256`
shows **100/100 hash matches**, with no missing or extra evidence files.

Three remote fixture runs each returned exit 0, 100,000 seeded posts,
5/5 indexed/spill-free query plans, and 2,000/2,000 HTTP 200 requests in
each of five cases (15 cells; 30,000 successful measured HTTP requests).
Raw p95 ranges: first feed c1 0.894–0.972 ms; cursor c1
0.924–1.112 ms; tag+score c1 2.700–2.739 ms; around c1
2.048–2.059 ms; around c8 3.726–3.975 ms. All three intentional
validator faults return exit 1 with actionable errors. Raw run result
JSON, EXPLAIN plans, failure logs and cleanup markers were inspected,
not just the agent's summary. No numerical performance budget justified.

**Acceptance blocker:** All three remote run `environment.txt` files and
`run*-fixture.log` trailers identify `tested_sha=5b574024dc9024a026d632434c01eec96b5577a1`
and `tested_branch=master`, **not** candidate SHA
`fa9df460c00b78175b748a08510c225b76592700`.
Although six source/fixture input paths have local/remote byte-identical
SHA-256 hashes, that does not prove the full Go API source tree built by
the remote harness matched the executable candidate. A scripted archive
transfer inside another Git checkout can benchmark the caller's other
repository because the original fixture used `git rev-parse --show-toplevel`
from the caller cwd. The 3-run evidence therefore demonstrates the
benchmark pattern and validator correctness but **does not confer
INDEPENDENT_EVIDENCE_PASS for the exact SHA**.
The findings document has a mistaken correctness job ID
`113493599564`; raw embedded job JSON and live GitHub API agree that
`113518519620` is correct for green candidate run `37837443059`.
The summary also incorrectly says 30 cells; there are 15 cells. These
are corrected here rather than silently accepted.
The evidence lacks a full before/during/after remote host-resource
series, a further limitation for numerical calibration.

**Coding fix:** On the same feature branch, the benchmark harness now
resolves its root relative to the script path; requires an actual clean
Git checkout at exactly `GINBAR_PERF_EXPECT_SHA` (or Actions
`GITHUB_SHA`), fails closed for wrong/missing SHA, and records both
actual/expected SHA in the environment. Documentation updated.
The **new executable/configuration candidate is**
`d25046f78e4343d7612f04fbff9f58dc9cb81f74` (prior
`fa9df460...` superseded). Docs/config preceding this commit are
included. The previous green run applies only to the superseded
executable and cannot authorize the new one. New exact-candidate CI
`v2 CI` workflow `374214168`, run `37844850570`,
feature branch `push`, initially `pending` when observed; job and
result unknown. Any later STATE-only commit is documentation-only.
No `v2` merge or deployment occurred.

**Gate:** IMPLEMENTED, pending new EXACT_CANDIDATE_CI_GREEN and
new independent acceptance. No numeric thresholds; M7 remains open.

**Exactly ONE next task:** Verify new exact-candidate CI for SHA
`d25046f78e4343d7612f04fbff9f58dc9cb81f74` and remedy any concrete
failure. Once GREEN, request **a fresh independent** LOCAL acceptance
run from a detached, tracked-clean worktree at **that exact SHA** with
`GINBAR_PERF_EXPECT_SHA` explicitly set and complete raw host evidence.
Do not replay acceptance based on the master-SHA runs and do not integrate
before the corrected independent gate passes.

### Corrected exact-candidate CI GREEN — 2026-10-08; independent acceptance next

**Verified live refs before this state update:** `v2` =
`36e27ed4f1671f7a50a3e99833ffb2fecb2c6e7f`; candidate branch
`astra/m7-performance-regression-gate` =
`00384ecd5d59387a20a729b522db698fedcf9b83` (STATE-only above
candidate). **Exact executable/configuration SHA**:
`d25046f78e4343d7612f04fbff9f58dc9cb81f74`.

**EXACT_CANDIDATE_CI_GREEN verified from live Actions run/job API and
independently fetched raw runner logs.** Workflow `v2 CI`
ID `374214168`, run `37844850570`, correctness job
`113543437071`; branch `astra/m7-performance-regression-gate`,
event `push`, exact `head_sha=d25046f78e4343d7612f04fbff9f58dc9cb81f74`,
run `completed/success` updated `2026-10-08T21:16:52Z`,
attempt 1; **8/8 job steps success**: setup, exact checkout, checkout SHA
verification, full scoped correctness, target worker release build,
tracked-checkout-unchanged, postcheckout, complete. Raw markers:
`v2-perf-regression: PASS shape=bounded/indexed/no-spill http=all-200 budget=not-yet-calibrated`,
`v2-perf-regression: exit=0 sha=d25046f78e4343d7612f04fbff9f58dc9cb81f74 results=... containers_removed=yes`,
and `v2-ci: PASS sha=d25046f78e4343d7612f04fbff9f58dc9cb81f74 scope=all`.
The corrected script's clean Git checkout/expected-SHA guard runs before
launching Docker; its successful exit on this run confirms guard acceptance
under Actions `GITHUB_SHA`. The ephemeral results directory path was
reported by the CI harness; retained independently transferable raw
results are **not** asserted from the CI run.

**CI synthetic-data measurements** (2,000 requests/cell, errors zero;
p95 / p99 in milliseconds, then RPS):
- first feed c1: **0.982 / 1.153 / 1236.0**;
- ID cursor feed c1: **1.091 / 1.316 / 1166.2**;
- tag+score c1: **2.670 / 3.377 / 519.8**;
- around-post c1: **2.351 / 2.766 / 530.5**;
- around-post c8: **7.623 / 8.985 / 1784.2**.

Five raw `EXPLAIN (ANALYZE, BUFFERS)` plans, in order: first feed
**0.220 ms**, ID cursor **0.247 ms**, tag+score **3.084 ms**,
tag+exclude+score **2.279 ms**, around-post **0.372 ms**.
Post/media indexed scans visible; no hot-table sequential scan or spill
failure. Numerical p95/p99/RPS budgets remain **uncalibrated** and
**unenforced**, rather than treating single-CI host numbers or old M2
measurements as universal budgets.

**Decision:** No executable repair warranted; gate now
`EXACT_CANDIDATE_CI_GREEN`. The prior ZIP
`perf-regression-20261008T202505Z.zip` does NOT become independent
acceptance because its `master`-SHA provenance mismatch remains.
Do not integrate or mark this M7 slice closed; M7 remains open.
No production DB/host, `master`, or integration ref was modified.

**Exactly ONE next task:** Ask LOCAL EXECUTION AGENT for a *new independent
acceptance* against a clean detached Git worktree at exact executable SHA
`d25046f78e4343d7612f04fbff9f58dc9cb81f74`, with
`GINBAR_PERF_EXPECT_SHA` explicitly set, three disposable PostgreSQL
benchmark runs and captured full-source provenance, errors/negative tests,
raw HTTP JSON, EXPLAIN plans, before/during/after host conditions and
cleanup proof. Return exactly one archive under `.local-agent-results/`
with `findings.md`, manifest hashes, external ZIP SHA-256 and path.
LOCAL must not edit tracked files, commit, merge, deploy or run on a
production host without new explicit approval. CODING must independently
inspect the new raw evidence before any `CODING_ACCEPTED`, guarded
fast-forward integration or distinct post-integration `v2` push CI.

### M7 performance regression: accepted exact executable integrated; post-integration CI pending — 2026-10-09

- **INDEPENDENT_EVIDENCE_PASS / CODING_ACCEPTED:** user explicitly
  authorized one disposable three-run acceptance on shared
  `amp.kejith.de` despite other live services. Received
  `m7-perf-shared-auth-20261008T224019Z.zip`, exact uploaded-byte
  SHA-256
  `3c768f4d92f2c15c456ba1c95a76d1a6834a741bc74c52cd6bdb7bb3bd7ac781`.
  Independent CRC check PASS; **133/133** manifest entries hash-match,
  no extra/missing entries. Independently audited actual commands
  and exits, clean detached source checkout at exact executable
  `5712176ceb2e6e886578bad71d1515ee6d0cec1a`, per-run
  `tested_sha=expected_sha`, raw SQL EXPLAIN/buffers, HTTP JSON,
  negative checks, hosts before/during/after and cleanup.
  Three fresh sequential first-attempt runs returned exit 0, each
  using a fresh disposable PostgreSQL 17.11 database with 100,000
  seeded posts, five index-backed SQL plans without hot-table
  sequential scans or spills, and 2,000/2,000 HTTP 200 for each
  of five cases. Total **30,000/30,000 successful requests**.
  Missing/wrong expected SHA both exited 2 before Docker resource
  creation. Original canonical checkout remained tracked-clean.
  Persistent container and network inventories identical before/
  after; during snapshot identified temporary isolated Go/API/PG
  resources; all temporary containers/network/cache/worktree
  removed. No source changes, deployment, existing service restart,
  persistent DB writes or production-data benchmark.
- Per-cell p95 intervals (ms) across three independent runs:
  first feed c1 `0.893–0.933`, cursor c1 `0.946–1.032`,
  tag+score c1 `2.701–2.771`, around c1 `2.013–2.117`,
  around c8 `3.726–4.290`. These provide reproducibility
  observations only, **not calibrated numeric budgets**.
  `GINBAR_PERF_BUDGET_FILE` remains unset. The structural
  correctness/measurement fixture is the accepted scope.
- Previously verified exact-candidate feature `v2 CI`,
  workflow `374214168`, run `37848213706`,
  correctness job `113554321060`, exact executable
  `5712176ceb2e6e886578bad71d1515ee6d0cec1a`,
  completed/success, 8/8 steps success.
- **INTEGRATED:** verified fresh live `v2` ref
  `36e27ed4f1671f7a50a3e99833ffb2fecb2c6e7f`
  and compare to accepted executable SHA: **ahead 14, behind 0**.
  Performed **non-force, expected-SHA** guarded ref update to
  `5712176ceb2e6e886578bad71d1515ee6d0cec1a`.
  GitHub update succeeded and re-read live `v2` confirms
  exact SHA. Feature branch documents-only HEAD
  `347c0a6fab9a878e162da799236389a54c960c57`
  was **not** integrated. This note is a following
  documentation-only `STATE.md` commit.
- **POST-INTEGRATION CI NOT YET VERIFIED:** distinct integration
  push started workflow `v2 CI` `374214168`, run
  **`37855607480`**, correctness job **`113578910879`**,
  event `push`, branch `v2`, exact executable SHA
  `5712176ceb2e6e886578bad71d1515ee6d0cec1a`,
  last observed **in_progress**, step `Run scoped v2
  correctness gate` in progress, result **unknown**.
  The feature-branch candidate CI must not substitute for
  this integration push CI. No closure until completed/success,
  all required steps green and exact SHA verified.

**Gate:** `IMPLEMENTED` → `LOCAL_FIXTURE_PASS` (CI fixture)
→ `EXACT_CANDIDATE_CI_GREEN` → `INDEPENDENT_EVIDENCE_PASS`
→ `CODING_ACCEPTED` → **`INTEGRATED`**;
`POST_INTEGRATION_CI_GREEN` and `CLOSED` pending. Overall M7
remains open outside this narrow performance fixture.

**Exactly ONE next task:** Verify distinct post-integration `v2`
push CI workflow `374214168`, run `37855607480`,
job `113578910879`, event/branch/exact SHA, all steps/result
and raw logs. If green, record `POST_INTEGRATION_CI_GREEN`
and close only this performance-regression fixture slice;
otherwise report the first actionable failure and route fixes
through a fresh exact-candidate gate. Do not start another
M7 slice or infer success from stale candidate CI.

### M7 performance-regression fixture: post-integration push CI GREEN and slice CLOSED — 2026-10-09

- **Exact integrated executable/configuration SHA:**
  `5712176ceb2e6e886578bad71d1515ee6d0cec1a`.
  Prior guarded non-force, expected-SHA fast-forward of
  `v2` moved from `36e27ed4f1671f7a50a3e99833ffb2fecb2c6e7f`
  to this exact executable SHA. Following previous documentation-only
  `docs/v2/STATE.md` update had HEAD
  `301579fee6561b2487584f823071cfffd168c61a`, which
  differs from executable only in STATE; this closing commit is also
  documentation-only.
- **POST_INTEGRATION_CI_GREEN independently verified** from fresh
  authenticated GitHub Actions run/job/step API and downloaded
  full raw job log (293,803 characters), distinct from
  feature-branch exact-candidate CI:
  - Workflow `v2 CI` ID `374214168`.
  - Run **`37855607480`**, attempt 1;
    event **`push`**, branch **`v2`**, head SHA
    **`5712176ceb2e6e886578bad71d1515ee6d0cec1a`**.
  - Run `completed/success`, updated
    **`2026-10-08T22:52:41Z`**.
    Correctness job **`113578910879`** `completed/success`;
    all **8/8** GitHub job steps completed/success, including
    exact revision checkout + SHA verification, full
    scoped correctness, target Rust worker release build,
    tracked-checkout-unchanged, postcheckout and complete.
  - Raw success markers:
    `v2-perf-regression: PASS shape=bounded/indexed/no-spill http=all-200 budget=not-yet-calibrated`,
    `v2-perf-regression: exit=0 sha=5712176ceb2e6e886578bad71d1515ee6d0cec1a ... containers_removed=yes`,
    and
    `v2-ci: PASS sha=5712176ceb2e6e886578bad71d1515ee6d0cec1a scope=all`.
  - Post-integration CI VM HTTP observations, p95/p99/RPS:
    first feed c1 `1.088/1.251/1139.1`,
    ID cursor c1 `1.278/1.582/986.6`,
    tag+score c1 `3.023/3.361/494.7`,
    around-post c1 `2.400/2.738/509.6`,
    around-post c8 `7.178/9.166/1838.3`.
    These are CI VM measurements only, not target SLOs.
- The independently reviewed authorized target acceptance archive
  remains `m7-perf-shared-auth-20261008T224019Z.zip`
  SHA-256
  `3c768f4d92f2c15c456ba1c95a76d1a6834a741bc74c52cd6bdb7bb3bd7ac781`,
  133/133 manifest entries matching, three exact-source
  first-attempt successful isolated runs, total 30,000/30,000
  HTTP 200 requests, 15/15 accepted indexed SQL plans,
  missing/wrong SHA rejection, and host cleanup proof.
  This acceptance is limited to structural regression checks
  and repeatable timing evidence; **no accepted numeric
  latency, p99, RPS or environment-independent threshold has
  been established**, and the gate does not claim one.
- **FINAL SLICE GATE:**
  `IMPLEMENTED → LOCAL_FIXTURE_PASS → EXACT_CANDIDATE_CI_GREEN
  → INDEPENDENT_EVIDENCE_PASS → CODING_ACCEPTED → INTEGRATED
  → POST_INTEGRATION_CI_GREEN → CLOSED`.
  M7 **performance-regression fixture slice CLOSED**.
  **M7 production hardening overall remains open** until all
  other STATE/PLAN requirements receive their own evidenced gates.
  No production deployment, modification to live databases or
  legacy `master` branch occurred.

**Exactly ONE next task:** CODING AGENT performs a narrowly
scoped **M7 numeric performance-budget calibration decision**
using accepted target-host raw runs from the retained independent
ZIP versus comparable CI fixture observations. Inspect variability
and background host load, propose target-/environment-specific
p95/p99/RPS budgets with explicit provenance and documented
tradeoffs, and decide whether the available evidence is adequate.
Do **not** silently insert speculative numeric thresholds into CI,
make schema/index changes, run production-data benchmarks or
start a new implementation before establishing a defensible
baseline and separate acceptance gate.

### M7 numerical performance-budget calibration assessment — 2026-10-09 (documentation only)

**Decision: NOT YET CALIBRATED.** No enforceable p95, p99 or minimum-RPS
limits are currently justified for target-host or CI environments. Retain
the already-CLOSED performance-regression fixture's **deterministic**
HTTP success/error, five SQL plan shape/buffer/no-spill and exact-source
provenance checks as the active gate. Keep
`GINBAR_PERF_BUDGET_FILE` unset; no budget schema or runtime behavior
has been modified. This calibration review does **not reopen** the
closed fixture acceptance/integration CI gates. Overall M7 remains open.

**State and source provenance:** fetched live `v2` at
`edd3f1a1b40b6045cf80935b6b7f91f8edadd857`, which
differs from accepted executable/configuration
`5712176ceb2e6e886578bad71d1515ee6d0cec1a`
only in `docs/v2/STATE.md`. Accepted exact-candidate feature push
`v2 CI` workflow `374214168`, run `37848213706`,
job `113554321060`, completed/success. The distinct
post-integration `v2` push run `37855607480`, correctness
job `113578910879`, exact executable SHA, 8/8 job steps,
completed/success, remains verified. This review changes documentation
only, not the executable tested by these runs.

**Accepted comparable target-host data** from the independently audited
ZIP `m7-perf-shared-auth-20261008T224019Z.zip`,
exact archive SHA-256
`3c768f4d92f2c15c456ba1c95a76d1a6834a741bc74c52cd6bdb7bb3bd7ac781`;
133/133 internal manifest hashes verified. The three fresh first-attempt
runs occurred within **2026-10-08T22:40:26Z to 22:42:53Z** on one
explicitly authorized shared host (`amp.kejith.de`), with exact
executable SHA, identical Go 1.25.0 and PostgreSQL 17.11 container
images, synthetic 100,000-post seed, identical five cases, 20 warmups,
2,000 measured successful HTTP 200 per cell, zero errors. Raw inputs:
`run{1,2,3}/http-*.json`, `run{1,2,3}/summary.json`,
`run{1,2,3}/explain.txt`, `run{1,2,3}/environment.txt`,
`versions.txt`, `resources-{global,run*}-*.txt`, negative tests
and cleanup evidence. Metrics below are **descriptive statistics**,
not pass/fail budget proposals. Spread = 100*(max/min-1) over 3 rounds.

| Case | Target p95 median ms | p95 range ms / spread | p99 range ms / spread | RPS range / spread |
| --- | ---: | --- | --- | --- |
| feed-first-c1 | 0.915 | 0.893–0.933 / 4.4% | 0.974–1.727 / 77.4% | 1320.6–1391.7 / 5.4% |
| feed-cursor-c1 | 0.957 | 0.946–1.032 / 9.1% | 1.166–1.308 / 12.1% | 1217.7–1293.4 / 6.2% |
| search-tag-score-c1 | 2.728 | 2.701–2.771 / 2.6% | 2.859–3.207 / 12.2% | 566.9–578.3 / 2.0% |
| around-50000-c1 | 2.050 | 2.013–2.117 / 5.2% | 2.221–2.382 / 7.3% | 604.1–631.1 / 4.5% |
| around-50000-c8 | 3.796 | 3.726–4.290 / 15.1% | 4.519–5.881 / 30.1% | 2639.0–2726.0 / 3.3% |

**CI measurements are an UNLIKE environment, not a pooled target
sample.** Independently re-read exact-SHA raw runner logs:
- Feature candidate `37848213706`/`113554321060`, five
  p95/p99/RPS: first c1 `1.276/1.692/1075.3`,
  cursor c1 `1.142/1.293/1097.5`,
  search c1 `2.949/3.432/507.4`,
  around c1 `2.328/2.675/537.0`,
  around c8 `7.237/9.079/1845.1`.
- Post-integration `37855607480`/`113578910879`,
  same five cases: first `1.088/1.251/1139.1`,
  cursor `1.278/1.582/986.6`,
  search `3.023/3.361/494.7`,
  around c1 `2.400/2.738/509.6`,
  around c8 `7.178/9.166/1838.3`.
  The CI c8 p95 is approximately **1.9x** the target-host
  median c8 p95, while throughput is lower. Runner VM, cgroup,
  background activity, scheduling and CPU profile are not
  controlled/equated to the authorized target host, so
  **no cross-environment threshold and no claimed speedup**.
  The historical M2 around c1/c8 p95 of 2.277/4.871 ms is
  likewise not a directly matched workload baseline.

**Why no defendable hard limits yet:**
1. Only three back-to-back target rounds (single short time window),
   not independent days/load states or an established noise envelope.
   Target first-feed p99 spans **77.4%**, around c8 p99 **30.1%**
   and c8 p95 **15.1%**. Even 2,000 observations inside a round
   do not independently sample host-to-host or time-to-time variance.
2. During-run `resources-run*-during.txt` mostly records
   `docker ps` service/container status, not time-aligned
   CPU pressure, per-service CPU/RSS, host runqueue/IO, contention
   or cgroup throttling at benchmark execution; before/after
   host memory and coarse preflight load cannot stratify latency
   outliers. Shared-host services remained unchanged, but their
   changing activity is a confounder.
3. Only two standalone CI runner snapshots at the exact SHA,
   with no repeated CI-environment distribution, hardware/runner
   equivalence contract, controlled idle/loaded pairing, or
   pinned runner performance class. Accepting target values for
   CI would produce foreseeable false-positive regressions.
4. A budget should be able to detect material regressions without
   noisy flaky failures, with documented sample distribution,
   threshold rationale, environment policy and intervention
   criteria. The evidence is descriptive, not enough to fit that
   policy. Therefore **proposed enforceable numeric limits:
   NONE** for p95, p99 or RPS, in any environment.

**Calibration policy for a separate decision (not activated):**
- Define separate profiles for an authorized target-host class and
  a specifically pinned CI runner class. Each requires matching exact
  synthetic seed, query/HTTP fixture, PostgreSQL/Go image digests,
  concurrency, API pool cap, CPU/cgroup class and workload settings.
- Collect **at least 10** full independent rounds per host/load
  condition across **at least three separate time windows**;
  observe quiet/typical-shared-load conditions, with intentional
  no-production-change policy. Log time-aligned host + relevant
  service CPU/RSS/loadavg/pressure/cgroup throttling and resource
  cleanup for **each HTTP cell**, not just a container-list snapshot.
  Compare raw p50/p95/p99/RPS distribution and within-condition
  variability; investigate tail outliers and check the first/last
  rounds for warm-cache/time drift.
- Analyze each environment separately. Only once the spread and
  acceptable load envelope are measured should CODING propose
  `schema: 1` budget JSON values in `maxP95Ms`,
  `maxP99Ms`, `minRequestsPerSecond` with exact evidence
  provenance, explicitly stated safety/noise headroom, version,
  scope and agreed false-positive policy. Retain structural
  hard failures; a first numeric breach should collect complete
  raw evidence, be reproduced in the same pinned environment,
  and be investigated against host contention before claiming
  an application performance regression. No automatic service
  tuning, schema/index change or production mitigation.
- A prior user approval on `amp.kejith.de` was **one-task-only and
  already consumed**. Any further shared-host measurements require
  **new specific operator authorization**; no background benchmark,
  production-data run, target-host write, or CI activation is
  authorized by this documentation decision.

**Exactly ONE next task:** CODING obtains **new explicit task-specific
authorization** for a controlled **LOCAL independent measurement-only
calibration gate** on an eligible isolated target/runner host,
including the above minimum per-condition rounds and time-aligned
resource observations. Only after the user approves may CODING
issue a narrowly permissioned LOCAL execution handoff at accepted
executable SHA
`5712176ceb2e6e886578bad71d1515ee6d0cec1a`
to collect a raw ZIP with versions, manifest/SHA-256, per-cell
timings, resource conditions and cleanup. Do not repeat the
previous completed acceptance, invent numerical budgets or
change code/CI until that separate measurement evidence is
reviewed and an explicit threshold policy accepted.

### Ten-round calibration ZIP review: usable diagnostics, formal calibration still blocked — 2026-10-09

**Uploaded evidence:** `m7-perf-calibration-20261008T230231Z.zip`,
exact uploaded-byte SHA-256
`5a8fa6ac7f792025676185c01c50a1d76ec958255939b410659de5ae7206a03d`,
size 224,814 bytes, **478 regular ZIP entries**. Independently verified
ZIP CRC (all entries readable); inspected raw `findings.md`,
`repo-state.txt`, `host/environment.txt`, each of 10 full round
results, individual HTTP JSONs, SQL EXPLAINs, timeseries, provenance,
and cleanup. **There is NO `MANIFEST.sha256` (or other file-level
hash manifest) in the uploaded ZIP**; therefore the mandatory 478-file
manifest reconciliation and separate archive packaging provenance
were **not satisfied**. The exact uploaded archive itself was hashed;
this is not a claim that an absent manifest passed.

**Raw technical result:** 10/10 fixture runs, all first-attempt exit 0,
at exact detached tracked-clean executable/configuration SHA
`5712176ceb2e6e886578bad71d1515ee6d0cec1a`, each
`expected_sha=tested_sha`, `GINBAR_PERF_BUDGET_FILE=none`,
PostgreSQL `17.11`, Go image `1.25.0`, seeded
100,000 synthetic posts. Each round: five HTTP JSON cases with
2,000/2,000 HTTP 200 and zero errors, five completed indexed
EXPLAIN ANALYZE plans with zero detected hot posts/media table
sequential scans or disk sort/spill. **50 HTTP cases,
100,000/100,000 successes, 50/50 acceptable SQL plans.**
Three LOCAL-defined windows are only about twenty minutes apart:
W1 `23:03:25–23:07:10Z` (rounds 1–4), W2
`23:12:19–23:15:00Z` (5–7), W3
`23:20:06–23:22:46Z` (8–10) on 2026-10-08;
there are two ~5-minute idle gaps. The experiment sampled one
quiet/typical shared-host condition, not independent days or
controlled competing-load regimes. During-run sampling is sparse
(~14–15 rows per ~52-s round, `docker stats --no-stream`
sampling slow) and is not precisely synchronized to all five HTTP
cell start/finish times. No new CI-runner baseline was measured;
the same prior exact-SHA feature/post-integration CI provenance
was included for reference only.

**Independently recomputed 10-round descriptive target-host data**
(min/median/max, all times milliseconds except RPS):
- `feed-first-c1`: p95 **0.872/0.899/1.182**,
  p99 **0.981/1.028/1.832**, RPS
  **1271.159/1366.916/1387.054**.
- `feed-cursor-c1`: p95 **0.932/0.971/1.014**,
  p99 **1.016/1.122/1.241**, RPS
  **1245.566/1280.803/1321.234**.
- `search-tag-score-c1`: p95 **2.724/2.763/2.798**,
  p99 **2.901/3.089/3.185**, RPS
  **546.141/560.601/576.146**.
- `around-50000-c1`: p95 **2.047/2.060/2.078**,
  p99 **2.264/2.321/2.370**, RPS
  **613.571/622.542/628.353**.
- `around-50000-c8`: p95 **3.783/3.913/4.567**,
  p99 **4.586/4.906/5.806**, RPS
  **2509.869/2703.455/2727.984**.
Spread `100*(max/min - 1)` is **86.7%** for first-feed
p99, **35.5%** for first-feed p95, and **26.6%** for
around-c8 p99; stable around-c1 p95 is only **1.5%**.
These are **descriptive diagnostics**, not accepted numeric
thresholds or CI-VM comparisons.

**Permission/gate blocker:** The prior operator's explicit
`amp.kejith.de` authorization was **one acceptance task**,
consumed by the 3-run archive
`m7-perf-shared-auth-20261008T224019Z.zip`.
Following closure, STATE explicitly required new task-specific
authorization before further shared-host calibration executions.
The new 10-run ZIP does not contain evidence of **fresh approval**,
and no such operator approval has been supplied in this chat.
Do not infer consent merely from a ZIP upload or retroactively
expand earlier permission. This archive's provenance can be
examined as a technical diagnostic but cannot count as a
permission-compliant separate independent measurement gate.
Cleanup records report temporary worktree/cache and
performance containers/networks gone, canonical tracked status
clean, and pre-existing services unchanged. Those observations
do not cure the authorization and manifest gaps.

**CODING calibration decision:** hard numerical `maxP95Ms`,
`maxP99Ms`, `minRequestsPerSecond` still **NOT ACCEPTED**
and **NOT ACTIVATED**, including on shared host and CI runner.
Retain structural correctness/SQL gate unchanged and
`GINBAR_PERF_BUDGET_FILE` unset. No production-data test,
schema/API/index change, master edit, release deployment,
or executable/CI change by CODING. The closed M7
structural performance-regression fixture stays CLOSED;
overall M7 remains open. Live `v2` reverified at
`e1717fafc464ddf201587fcf22bf8a0e2ba293c3` before
this documentation-only STATE commit; the accepted
executable remains
`5712176ceb2e6e886578bad71d1515ee6d0cec1a`
with exact post-integration `v2` push CI run
`37855607480` / job `113578910879`,
completed/success on that exact SHA.

**Exactly ONE next task:** Obtain **new explicit operator approval**
for a **separate, bounded LOCAL measurement-only calibration gate**
on `amp.kejith.de` or a genuinely non-production test host,
with measurements in meaningfully separated time/load windows,
safe resource envelope and no production-data access. Only
after approval issue a narrow LOCAL handoff requiring a detached
clean exact-SHA checkout, raw per-cell results and precisely
timestamped host/service pressure observations, a complete
`MANIFEST.sha256`, separately computed uploaded ZIP SHA-256,
and cleanup proof. Do not repeat another execution with unchanged
permissions, accept a numeric budget, or start another M7
implementation slice before that gate is resolved.

## M7 performance calibration — operator-limited two-window closeout (2026-10-09)

**Decision: close the optional numerical-calibration measurement campaign as FINAL-TRUNCATED / DIAGNOSTIC-REVIEWED; numerical thresholds NOT ACCEPTED and NOT ACTIVATED. No further calibration runs requested.** This is an operator-directed stop, not a 10-round / 3-window compliance PASS. The already CLOSED structural PostgreSQL/API performance fixture remains CLOSED; overall M7 remains in progress.

**Independently inspected uploaded bytes:** `m7-perf-calibration-20261009T103500Z.zip`, SHA-256 `2af1f59f9d1748344e0e5e147a500e00a52f8320f474ceb5356844dacbcda3fd`, 241 entries, ZIP CRC clean. Exact tested detached-clean executable/configuration SHA `5712176ceb2e6e886578bad71d1515ee6d0cec1a`; live `v2` at review `44cd31051a8c977fb7d2ffb8a4a48ca70e74a1cd`, four docs-only commits ahead, only `docs/v2/STATE.md` changed. Applicable prior post-integration `v2 CI` workflow `374214168`, run `37855607480`, job `113578910879`, exact executable SHA `5712176ceb2e6e886578bad71d1515ee6d0cec1a`, push on `v2`, completed/SUCCESS. This documentation decision creates no new executable candidate; do not claim new CI.

**Measured, independently rechecked from raw JSON:** 7/7 fixture rounds exit 0 in two time windows, 35/35 individual HTTP cases, 70,000/70,000 measured HTTP 200, zero errors; 100,000 synthetic posts per disposable round; five bounded indexed EXPLAIN query blocks per round (35 total), no hot posts/media/post_tags sequential scan, no external sort or spill. The only sequential scans were over the tiny 100-row `tags` table, two per round. PostgreSQL 17.11 and Go image 1.25.0; `budget_file=none`.

**Observed raw p95 min/median/max, milliseconds (not budgets):**
- `feed-first-c1`: 0.867 / 0.908 / 0.987; p99 0.963–2.074.
- `feed-cursor-c1`: 0.928 / 0.964 / 1.109; p99 1.003–1.659.
- `search-tag-score-c1`: 2.429 / 2.706 / 2.920; p99 2.816–3.319.
- `around-50000-c1`: 1.972 / 2.018 / 2.175; p99 2.044–2.645.
- `around-50000-c8`: 63.876 / 79.331 / 80.100; p99 67.901–84.025, RPS 270.2–971.8. This gate enforced a two-CPU container cap, saturating PostgreSQL in c8 and making the c8 workload **not comparable** to prior uncapped ~3.9ms p95 target-host measurements. Round 6 c8 had a bimodal distribution (p50 3.105ms, p95 63.876ms); the observation is retained, not excluded.

**Timing:** W1 rounds 1–3 started ~10:35Z; W2 rounds 4–7 started ~13:05Z on 2026-10-09. W2 began ~2.5 hours after W1 (operator waiver of original six-hour minimum); W3 was cancelled by operator; no first-to-last 24-hour separation. Both windows sample one shared-host setting and do not establish independent-day or runner-class variance.

**Evidence/provenance limitations:** Uploaded ZIP lacked `MANIFEST.sha256` and separately retained raw final cleanup-command outputs; `findings.md` reports cleanup but independent proof was not bundled. A reviewer-generated 241-entry file-hash index for uploaded members exists outside this ZIP, not as original packaging provenance. Host/service telemetry samples are not individually aligned to every HTTP cell, and the original fresh authorization for W1 is not proven in this archive. The operator's recorded W2 early waiver and W3 cancellation do not retroactively prove W1 authorization. The diagnostic observations therefore **must not** be promoted to a formally permission-compliant independent calibration acceptance.

**Operator instruction 2026-10-09:** proceed using only these two windows; do not require, schedule or execute a third window or repeat benchmarks. Close *this measurement campaign* with the above documented limitations, not with invented green numeric gates. Keep `GINBAR_PERF_BUDGET_FILE` unset and do not add `maxP95Ms`, `maxP99Ms` or `minRequestsPerSecond` thresholds, revise CI, alter application/config/schema, or infer a production speedup. No production host changes or further test execution by CODING.

**Exactly ONE next task:** CODING conducts a planning-only review of the remaining M7 PostgreSQL backup/restore and recovery boundary, with fresh STATE/live-ref verification and a single narrowly scoped proposed implementation gate. Do not conflate this deferred numerical-calibration campaign with the closed structural correctness gate.


## M7 PostgreSQL operational recovery boundary — planning review (2026-10-09)

**Status: PLANNING COMPLETE; implementation not started.** Fresh GitHub resolution verified live `v2` at `68ae5849cbdcccb0385d7e2c29ce9c4054838976`. The accepted executable/configuration remains `5712176ceb2e6e886578bad71d1515ee6d0cec1a`; comparison from that executable to live `v2` is five commits ahead / zero behind and changes only `docs/v2/STATE.md`. This section is documentation-only and creates no executable candidate.

**Applicable accepted executable CI rechecked:** `v2 CI` run `37855607480`, correctness job `113578910879`, completed/SUCCESS. Raw job logs show checkout and gate SHA `5712176ceb2e6e886578bad71d1515ee6d0cec1a`, `v2-pg-backup-test: PASS`, and `v2-ci: PASS sha=5712176ceb2e6e886578bad71d1515ee6d0cec1a scope=all`. No CI is claimed or required for this planning-only STATE change.

**Existing closed primitive retained:** `scripts/v2-pg-backup-test.sh` already uses PostgreSQL custom format (`pg_dump -Fc`), applies every checked-in migration in deterministic filename order, seeds FK-linked rows and non-default identities, compares deterministic public-table and sequence snapshots, compares native archive TOC schema inventories, restores strictly with `pg_restore --exit-on-error --no-owner --no-privileges`, and transactionally checks restored identity/FK usability. That logical backup/restore correctness gate remains CLOSED and must not be reopened or replaced by a weaker parallel fixture.

**Missing operational boundary identified:** there is no operator-facing artifact command that securely creates, validates and atomically publishes a logical backup plus credential-free integrity/version metadata; no tracked recovery drill exercises that exact operator path through source loss into a fresh database; and current CI does not prove destination collision, archive corruption, failed/interrupted creation cleanup, credential non-disclosure, or completed-looking-artifact fail-closed behavior. The deployment rollback fixture remains mock orchestration only and does not establish database migration rollback.

**Narrow implementation gate selected:** add an operator-invoked native PostgreSQL logical-backup command (preferred `scripts/v2-pg-backup.sh`) and a disposable PostgreSQL 17.11 cold-recovery fixture (preferred `scripts/v2-pg-recovery-test.sh`) that calls the real operator command. The backup command must use standard libpq inputs without logging credentials, `umask 077`, same-filesystem temporary output, `pg_dump -Fc`, native `pg_restore --list` validation, SHA-256/size/UTC/tool+server version metadata without secrets, atomic final publication only after validation, collision refusal, and signal/failure cleanup with no completed-looking output. The recovery fixture must reuse/factor the existing snapshot/sequence/TOC/FK+identity checks, prove source non-mutation, simulate source loss/unavailability, restore into a new empty database using strict `pg_restore` flags, and cover corrupt/truncated restore failure, collision refusal, interrupted/failed creation cleanup, credential non-disclosure, permissions/metadata integrity, and complete disposable cleanup.

**CI/documentation scope:** wire the new recovery fixture into full-scope `scripts/v2-ci.sh` and `.github/workflows/v2-ci.yml` only as required, and document the operator procedure and limitations under v2 operations/recovery documentation. Keep the existing `v2-pg-backup-test.sh` unless factoring shared helpers is materially cleaner; do not weaken its assertions.

**Explicit exclusions/risks:** no scheduler/timer, retention deletion, off-host storage/upload, object storage, encryption/key management, WAL/PITR, replication, production-data or production-host execution, RPO/RTO guarantee, schema/index/database change, migration rollback machinery, deployment automation, numerical performance calibration/budgets, or legacy `master` change. This future gate will prove logical artifact creation and cold restore mechanics only. Effective RPO remains bounded by the age of the latest independently durable backup; a local dump does not protect against host loss; large-database duration/disk/IO impact remains unestablished; deployment rollback across migrations remains separate; privileged database credential handling requires explicit review.

**Execution capability note:** this CODING environment has GitHub repository write and CI-read capability but no local repository checkout, Docker, `psql`, or `pg_restore`. Implementation can be authored remotely, but fixture execution must occur on a capable disposable executor; lack of local execution must not be represented as a pass.

**Exactly ONE next task:** CODING implements the bounded PostgreSQL operational logical-backup + disposable cold-recovery gate above on a new isolated branch from the then-live `v2` (suggested `astra/m7-pg-operational-recovery`), obtains tracked fixture evidence in a capable disposable environment and exact-candidate `v2 CI` GREEN, updates STATE with exact candidate/workflow/run/job/SHA/result evidence, and only then issues a separate LOCAL EXECUTION AGENT INDEPENDENT ACCEPTANCE handoff. Do not integrate before that independent evidence is reviewed and accepted.


## M7 PostgreSQL operational recovery — candidate awaiting diagnostic execution (2026-10-09)

**Gate: CODE AUTHORED / UNVERIFIED, not IMPLEMENTED and not accepted.** Candidate branch `astra/m7-pg-operational-recovery` starts from freshly verified live `v2` base `41f40568267d68fbeb04dd81c9f2085d55a8bb25` (documentation-only). Candidate executable/configuration SHA `77cc704f86afe57bd7ea7d28d93acddaff21deec`. This STATE edit is documentation-only above that executable SHA. Integration `v2` and `master` are unchanged by this candidate branch.

**Authored scope:** `scripts/v2-pg-backup.sh` implements operator-invoked libpq logical custom-format backup, private staging directory, archive validation, credential-free checksum/size/UTC/version metadata and no-clobber directory publication with failure/signal cleanup. `scripts/v2-pg-recovery-test.sh` exercises this command against disposable PostgreSQL 17.11, migrations/FK identities, row+sequence snapshots, TOC inventories, source loss, strict cold restore, collision/corrupt/injected-failure/interruption negatives and cleanup. Full-scope CI wiring and operations documentation updated. No production environment or numerical performance calibration was touched.

**Execution evidence status:** no Docker daemon/client, `psql` or `pg_restore` available to CODING; no executable checkout in this runtime. No successful `bash -n`, Docker fixture or full `v2-ci.sh` run is claimed. Exact-candidate CI workflow/run/job/result is **unknown**; GitHub PR-only commit workflow lookup returned an empty list, which is not proof of absence of push CI. The only previously confirmed green CI remains run `37855607480`, correctness job `113578910879`, on previously accepted executable `5712176ceb2e6e886578bad71d1515ee6d0cec1a`, not the new candidate.

**Exactly ONE next task:** LOCAL EXECUTION AGENT performs **DIAGNOSTIC EXECUTION ONLY** on a detached clean checkout of candidate executable SHA `77cc704f86afe57bd7ea7d28d93acddaff21deec`, using disposable PostgreSQL 17.11. Run Bash syntax checks and the new recovery fixture, preserve the first actionable failure with raw logs or a complete PASS if actually obtained, and read exact-candidate push CI provenance if available. Return one evidence ZIP with SHA, versions, commands, raw evidence and cleanup. This diagnostic is not independent acceptance and cannot establish `EXACT_CANDIDATE_CI_GREEN`; CODING must repair/verify first. No integration permitted.
