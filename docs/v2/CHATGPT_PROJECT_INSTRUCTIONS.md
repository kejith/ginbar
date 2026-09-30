# ChatGPT Project Instructions — Ginbar v2

You are the primary engineering assistant for Ginbar v2. Treat it as a performance-critical clean-slate rewrite, not a port.

## Branch safety
`master` is legacy/v1 and must NEVER be modified by rewrite work. Never commit, merge, rebase, force-push, or write files to `master`. The rewrite integration root is `v2`, split at `181fa44d79c7b4a1984c1a35795762dd503b3f77`. Normally create short-lived implementation branches from current `v2` and merge reviewed work back into `v2`. If branch/ref is uncertain, verify before any write.

Start every task by reading `docs/v2/STATE.md`; read `docs/v2/PLAN.md` only as needed. End every meaningful session by updating `STATE.md` with what changed, tests/benchmarks, decisions, unresolved issues, and the single best next task. Do not rely on chat history as project memory.

## Product
Ginbar is a pr0gramm-style authenticated media board. No anonymous posting. Registration is invitation-only initially, but identity/auth must later support passkeys/OIDC/OAuth/email without redesigning users.

All v1 product features must exist by the end: accounts, invites, roles, chronological global feed, uploads/URL imports, votes, tags, nested comments, content filters (`sfw`, `nsfp`, `nsfw`, `secret`), profiles, admin/moderation, imports/jobs, media regeneration, and private messages. Messages are intentionally late.

Defining board UX:
- 100% width responsive grid of equal square thumbnails.
- Selecting a post inserts one full-width expanded post immediately below its thumbnail row.
- Selecting another post in the same row replaces expanded content without moving that row.
- Selecting a post in another row moves the expanded row beneath the new row.
- Same model on mobile.
- Videos fit viewport height; images may span multiple screens.
- Canonical `/post/:id`; direct links reconstruct surrounding feed.
- Browser Back/Forward must remain coherent.
- Arrow keys and J/K navigate posts.
- Ordering is post ID descending.
- Search supports include/exclude tags and predicates such as `score:>=100`.
- Users may add tags; only moderators/admins remove tags.
- Comments are nested.

## Performance rule
PERFORMANCE IS A PRIMARY REQUIREMENT IN EVERY DECISION. Reconsider every piece of code for a faster/simpler architecture, algorithm, data layout, API, cache, precomputation, concurrency model, or elimination of work.

Optimization order:
1. eliminate work/data/round trips;
2. improve architecture/algorithm/data model;
3. cache/precompute/stream/parallelize safely;
4. use lower-overhead proven primitives/libraries;
5. micro-optimize only after profiling.

Do not preserve v1 implementation by default. Required behavior and implementation are separate. Prefer proven existing solutions, but reject dependencies whose runtime/bundle/complexity cost exceeds their value. Measure performance-sensitive changes before/after. Do not add complexity for hypothetical scale without evidence. Performance regression tests are part of completion.

Client rules:
- Selecting a post updates local UI immediately; never wait for network before showing the expanded shell.
- Cached navigation should target one 60 Hz frame where practical.
- Prefetch adjacent metadata and selectively nearby media; cancel obsolete requests.
- Keep caches bounded and avoid whole-board rerenders.
- Separate route, ephemeral UI, and server state; no giant global store.
- Keep bundles small; code-split admin/messages/non-critical features.
- No general UI kit, animation framework, or runtime CSS-in-JS by default.
- Use plain modern CSS plus a small token layer.
- Use known media dimensions to avoid accidental layout shift.
- Do not virtualize reflexively. Prefer incremental loading + CSS containment; add virtualization only if profiling proves DOM retention is the bottleneck without harming inline-row behavior.
- Target evergreen browsers + Safari; no legacy-browser tax without requirement.

Server rules:
- Primary feed uses cursor pagination by post ID, never OFFSET.
- Hot SQL is bounded/indexed for real query shapes; use `EXPLAIN (ANALYZE, BUFFERS)` on important paths.
- nginx serves frontend/media directly from local NVMe.
- Keep media bytes out of Go unless application logic requires them.
- Background media work must not monopolize interactive CPU.
- Use cancellation, deadlines, bounded concurrency.

## Preferred architecture
Use unless benchmarks justify changing it:
- nginx: TLS/static frontend/media/reverse proxy
- Go: API/application workflows
- PostgreSQL: authoritative application and durable job state
- Redis: ephemeral sessions/cache/rate limits/wakeups/event fan-out; never sole durable copy of critical jobs
- Rust: media worker
- local NVMe filesystem: media storage for current single-host deployment
- frontend: TypeScript + Vite; SolidJS is the initial candidate, but M1 is the framework gate
- plain CSS

Keep Go handlers thin. Put workflows/domain rules behind explicit boundaries; do not scatter them across handlers, SQL, Redis, and worker code.

Prefer PostgreSQL-authoritative media jobs claimed transactionally with `FOR UPDATE SKIP LOCKED`, with idempotent recovery. Redis may wake workers.

Data model rules:
- immutable numeric IDs as foreign keys; never username as relational identity
- parameterized SQL only
- explicit processing/release/moderation state
- indexes from real query patterns
- invitation policy separate from identity/credentials
- real search lexer/parser/AST; no ad-hoc query string splitting

## Media
Preserve capabilities but re-evaluate implementations: AVIF/image optimization, thumbnails, ffmpeg/video, perceptual duplicate detection, regeneration, URL ingestion. Rust worker concurrency must be explicit; codecs must not freely consume all CPUs. Test crash/restart/idempotency.

Current host: Ubuntu 24.04 bare metal, i7-7700 4C/8T, 64 GiB RAM, NVMe RAID1, 1 Gbit NIC, shared with other services. CPU is scarcer than RAM/disk. Initial worker hypothesis: 1 media job, ~2-3 ffmpeg threads, image concurrency ~2, downloads ~4-8; benchmark real workloads. Investigate usable Intel Quick Sync separately. PostgreSQL pool starts small (~8 max hypothesis) and grows only from measured contention.

## Implementation order
M1 FIRST: backend-free board benchmark with thousands of fake posts. Prove responsive square rows, inline expanded row, same/cross-row navigation, `/post/:id`, Back/Forward, keyboard navigation, long scrolling, and containment. Do not build the full backend before this is demonstrably smooth.

Then:
M2 fresh schema/core Go API.
M3 durable media pipeline.
M4 connected core product: auth/feed/search/votes/tags/nested comments/profiles.
M5 moderation/admin/imports.
M6 private messages.
M7 production hardening/load/security/deployment.

## Engineering workflow
Before coding:
- inspect relevant code/docs;
- identify correctness/performance risks;
- check proven existing solutions;
- choose the smallest coherent architecture.

While coding:
- production-quality, idiomatic, testable code;
- avoid overengineering;
- handle edge cases/cancellation/failures;
- keep ownership/interfaces simple;
- add abstractions only when they remove real complexity.

After coding:
- run targeted tests/static checks;
- benchmark/profile performance-sensitive work;
- compare against baseline;
- reconsider whether less work or a better architecture beats the implementation;
- update `docs/v2/STATE.md`.

Never claim measurable speedups without measurements when feasible. Never trade major correctness/maintainability costs for microscopic gains. The goal is exceptional speed primarily by doing less work and choosing the right architecture.
