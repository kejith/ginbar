# Ginbar v2 Rewrite Plan

Status: planning / no v2 implementation started
Last updated: 2026-09-30
Rewrite root: `v2`

## 1. Branch safety

- `master` is the v1/legacy branch. **Never commit, merge, rebase, force-push, or otherwise modify `master` during the rewrite.**
- `v2` is the permanent integration/root branch for the rewrite. It was split from `master` at commit `181fa44d79c7b4a1984c1a35795762dd503b3f77`.
- Implementation work should normally happen on short-lived branches created from the current `v2`, then be reviewed/benchmarked before merging back to `v2`.
- Never use `master` as an implementation base after the split. Consult v1 code only as behavioral/reference material.
- If branch context is uncertain, stop writes and verify the current branch/ref first.

## 2. Product definition

Ginbar v2 is a clean-slate pr0gramm-style media board. Existing v1 data may be discarded. Preserve the useful product behavior, not the old implementation.

All current features must exist by the end of the rewrite:

- accounts; no anonymous posting
- invitation-only registration initially
- architecture that can later add passkeys/OIDC/OAuth/email-based identity without redesigning users
- chronological global media feed
- responsive equal-square thumbnail grid
- inline full-width expanded post directly below the thumbnail row containing the selected post
- canonical `/post/:id` URLs and history navigation
- deep links reconstruct the feed around the selected post
- keyboard post navigation using arrows and J/K
- image/video posts, URL imports and direct uploads
- AVIF/image optimization, thumbnails, video processing, duplicate detection and regeneration jobs
- post votes, comment votes and tag votes/metadata behavior where applicable
- tags; users may add tags, only moderators/admins remove tags
- `sfw`, `nsfp`, `nsfw`, `secret` content filters with current access semantics
- search with required/excluded tags and structured predicates such as `score:>=100`
- nested comments similar to pr0gramm
- user profiles, roles, admin/moderation tools, imports and jobs
- private messages, implemented late in the rewrite

## 3. Primary UX invariant

The board interaction is the defining feature and must drive architecture.

- Grid consumes 100% available width.
- Thumbnails are equal-size squares.
- Column count changes responsively; thumbnail size is derived from viewport/container width.
- Selecting a thumbnail inserts exactly one expanded, full-width post row immediately after that thumbnail's grid row.
- Selecting another item in the same row replaces expanded content without moving the expanded row.
- Selecting an item in another row moves the expanded row beneath the new row with minimal visual disturbance.
- Mobile uses the same inline-row model, not a separate full-screen UI.
- Videos are constrained to the viewport; images may span multiple screens vertically.
- Closing a post does not need to restore the exact pre-open scroll position.
- `←/→` and `J/K` navigate posts. Navigation must remain coherent with browser Back/Forward and `/post/:id`.

The first implementation milestone is a backend-free board benchmark with thousands of fake posts. Do not build the full application until this interaction is demonstrably smooth.

## 4. Performance is the primary non-functional requirement

Every implementation decision must be revisited for architecture-level performance opportunities before accepting local code. Prefer removing work over optimizing work.

Order of optimization preference:

1. eliminate unnecessary work/data/round trips
2. choose a better algorithm/data model/API boundary
3. exploit caching/precomputation/streaming/parallelism safely
4. use a lower-overhead library/runtime primitive
5. micro-optimize only after profiling identifies a real hotspot

Rules:

- Measure before and after meaningful performance changes.
- Never justify complexity with hypothetical scale alone; record evidence/benchmark rationale.
- No framework/UI dependency without a concrete benefit that outweighs bundle/runtime cost.
- No general-purpose component library or animation framework by default.
- Avoid runtime CSS-in-JS; use plain modern CSS and a small token layer.
- Keep initial/client route bundles aggressively small and code-split non-critical features.
- Opening a post must update local UI immediately; never block the expanded shell on a network request.
- Prefetch adjacent post metadata and selectively prefetch nearby media.
- Cancel obsolete navigation/data requests.
- Keep server-state caches bounded; do not build one giant global client store.
- Avoid whole-board rerenders for a single post/vote/tag/comment update.
- Use known media dimensions to prevent accidental layout shift.
- Do not virtualize by reflex. First use incremental loading and CSS containment; add virtualization only if profiling proves retained DOM is the bottleneck and it does not damage inline-row behavior.
- Primary feed pagination is cursor-based by post ID. No OFFSET pagination for the board.
- SQL must be bounded, indexed for real query shapes and inspected with `EXPLAIN (ANALYZE, BUFFERS)` for important paths.
- Static media and frontend assets are served directly by nginx, never proxied through the application without a specific reason.
- Background media processing must not steal latency from interactive traffic.

Initial performance targets (benchmarks may tighten them):

- local cached open/next/previous interaction: complete UI state transition within one 60 Hz frame where practical
- no avoidable main-thread long tasks during normal board navigation
- no network dependency before visual response to selecting a post
- API hot paths designed for low single-digit/low tens of milliseconds on the target host under realistic load
- bounded memory growth during long scrolling/navigation sessions
- media jobs explicitly concurrency-limited and benchmarked independently from API latency

Performance regression tests/benchmarks are part of feature completion, not optional cleanup.

## 5. Preferred architecture

Keep the proven specialization of the current system, but redesign boundaries cleanly.

```text
Browser
  |
  v
nginx ------------------> immutable/static media on local NVMe
  |---------------------> built frontend assets
  `---- /api -----------> Go API
                            |-- PostgreSQL (authoritative application state)
                            |-- Redis (ephemeral state / wakeups / cache)
                            `-- media-job creation
                                   |
                                   v
                              Rust worker
```

### Backend

- Go.
- Keep handlers thin: HTTP parsing/auth/response only.
- Explicit application/service layer for workflows such as upload -> process -> tag/comment -> release.
- Domain logic must not be scattered arbitrarily across HTTP handlers, SQL helpers and Redis helpers.
- PostgreSQL is authoritative.
- Redis is for ephemeral responsibilities: sessions, short-lived caches, rate limits, vote buffering if benchmarks justify it, and worker/event wakeups.
- Prefer PostgreSQL-authoritative media jobs claimed with transactional `FOR UPDATE SKIP LOCKED`; Redis may notify/wake workers but should not be the only copy of job state.
- API compatibility with v1 is not a goal unless an external consumer is later identified.

### Data model

- Use numeric immutable foreign keys (`user_id`, etc.), never username as relational identity.
- Keep public/post ordering by post ID descending unless a concrete requirement changes it.
- Preserve soft deletion where needed for moderation/audit behavior.
- Model post processing/release state explicitly.
- Keep content filter as first-class post metadata while allowing current tag-driven recalculation rules.
- Design indexes from actual board/search/moderation query shapes.

### Authentication

Invitation-only is a registration policy, not the identity model.

Model users separately from credentials/identities so later authentication providers can be added cleanly. Start with username/password + invite. Password hashing choice must be benchmarked and configured safely for the target host.

### Search

Define a real parser/AST rather than splitting strings ad hoc. Required initial semantics:

- include tags: `cat landscape`
- exclude tags: `-anime`
- score predicate: `score:>=100`

Grammar should be extensible without changing the API shape. Compile validated AST to parameterized SQL; never interpolate query syntax into SQL.

### Frontend

Initial candidate: SolidJS + TypeScript + Vite, with plain CSS. This is a preferred hypothesis, not dogma. The board prototype is the framework gate: retain Solid only if measurements and implementation quality justify it. Compare another implementation only if the prototype exposes a credible concern.

Frontend rules:

- route/URL state, ephemeral UI state and remote/server state remain conceptually separated
- no giant application store
- board rows are first-class layout objects
- the selected post maps deterministically to a row index
- expanded post rendering is isolated from unrelated grid items
- optimistic interaction where correctness permits (votes, lightweight metadata actions)
- browser history is part of the state model, not bolted on afterward
- accessibility/keyboard behavior is implemented with the interaction, not retrofitted

### Media worker

- Rust.
- Preserve/reevaluate existing image/video expertise: AVIF, thumbnails, ffmpeg, perceptual duplicate detection, regeneration.
- Reuse algorithms only after reviewing whether v2 can simplify or outperform them.
- Explicitly cap concurrency/threading; libraries must not freely consume all CPUs.
- Prefer native/in-process processing when it is measurably faster/reliable; subprocesses are acceptable where they remain the best implementation.
- Crash recovery/idempotency must be inherent in the job model.

## 6. Target host assumptions

Current deployment target (2026-09-30):

- Ubuntu 24.04 bare metal
- Intel i7-7700, 4 physical cores / 8 threads, up to 4.2 GHz
- 64 GiB RAM
- two ~477 GB NVMe drives in RAID1; measured ~1.7 GB/s sequential write and ~3.3 GB/s sequential read
- 1 Gbit/s full-duplex NIC
- host also runs GitLab/game/other services

Implications:

- CPU is more constrained than RAM or local disk.
- Network will generally bottleneck static media before NVMe throughput.
- Media encoding must be background-prioritized and concurrency-limited.
- Initial media-worker target: 1 media job at a time; roughly 2-3 ffmpeg threads for video; image concurrency around 2; download concurrency around 4-8. Benchmark real pipeline before finalizing.
- Investigate usable Intel Quick Sync H.264/HEVC acceleration separately; ffmpeg listing QSV support does not prove this host/device is usable.
- Keep PostgreSQL connection pools small initially (roughly 8 max is a starting hypothesis) and increase only from measured connection contention.

## 7. Milestones and gates

### M0 - Repository/handoff foundation

- create/verify `v2` root
- add this plan and `STATE.md`
- add ChatGPT project instructions
- no product code yet

Gate: another session can recover exact status and next action from docs alone.

### M1 - Board performance prototype

Backend-free static/fake dataset with thousands of posts.

Implement and benchmark:

- responsive full-width square grid
- deterministic row grouping
- inline expanded row
- same-row and cross-row selection
- image/video sizing rules
- `/post/:id` + Back/Forward
- arrow + J/K navigation
- incremental loading/retention behavior
- fast long scrolling
- CSS containment strategy

Gate: interaction is consistently smooth on desktop and mobile-class emulation; profiling shows no architectural red flags. Decide/lock frontend framework here.

### M2 - v2 schema + core Go API

- fresh migrations/schema
- users/roles/invitations/credentials
- posts/media metadata/filters/release state
- tags and tag search AST
- cursor feed and around-post/deep-link endpoints
- comments/votes foundations
- media job table/state machine
- strict API contracts and error model

Gate: benchmark representative feed/search/post endpoints and query plans.

### M3 - Media pipeline integration

- upload and URL ingestion
- Rust worker claims durable jobs
- image/AVIF/thumbnail pipeline
- video processing
- perceptual duplicate detection
- release workflow
- regeneration jobs
- progress/status UI

Gate: crash/restart tests + real media benchmark + prove background workload does not materially degrade interactive latency under configured limits.

### M4 - Core product UX

- auth/invite registration
- board connected to API
- filters
- search grammar
- votes
- tags
- nested comments
- profiles

Gate: end-to-end board performance budgets remain satisfied.

### M5 - Moderation/admin/imports

- role management
- tag removal/moderation
- post/comment moderation
- imports
- job/admin observability

### M6 - Private messages

Implement messages only after board/core/admin architecture is stable.

### M7 - Production hardening

- nginx/static caching and compression
- process/file limits
- DB/Redis config based on load tests
- security review
- rate limits/abuse controls
- backup/recovery strategy
- observability
- load/performance/regression suite
- deployment/update procedure

## 8. Required engineering workflow

For each meaningful task:

1. Read `docs/v2/STATE.md` first, then only relevant sections of this plan.
2. Verify work is based on `v2`, never `master`.
3. State the concrete performance/correctness risk being addressed.
4. Inspect existing v1 code only when it provides useful behavioral or algorithmic evidence.
5. Check for proven libraries/platform primitives before writing custom infrastructure.
6. Implement the smallest coherent change.
7. Test correctness, edge cases and failure/restart behavior where relevant.
8. Benchmark/profile performance-sensitive code; compare against a baseline.
9. Reconsider whether a different architecture/algorithm can remove more work.
10. Update `STATE.md` with exactly what changed, measurements/decisions, unresolved questions and the single best next task.

Do not leave critical context only in chat history or commit messages.

## 9. Definition of done for any performance-sensitive feature

A feature is not done merely because it works. It must also:

- have a clear ownership/boundary in the architecture
- avoid unnecessary allocations/renders/queries/round trips
- have appropriate indexes/cache policy/concurrency limits
- handle cancellation/timeouts/errors correctly
- have tests for important behavior
- include a benchmark/profile or explicit reason measurement is not useful
- not regress established performance budgets
- update handoff state so the next session can continue immediately
