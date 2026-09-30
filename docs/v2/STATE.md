# Ginbar v2 State / Handoff

Last updated: 2026-09-30
Phase: M0 complete; M1 not started
Integration branch: `v2`
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the minimal resume point for humans and GPT-6 Astra. Read it before `PLAN.md`. Keep it short and update it after every meaningful work session.

## Current status

- `v2` exists and was split from `master` at `181fa44d79c7b4a1984c1a35795762dd503b3f77`.
- `master` has not been modified for the rewrite and must remain untouched.
- No v2 product implementation has started.
- Durable rewrite plan added in commit `924439ea8b1852d1ce133bcf6598831d173182df`.
- ChatGPT project instruction prompt added in commit `19b5cb6f6a99f4d14c875ba023de1865e66b240e`.
- Architecture/product/performance decisions below are accepted working decisions unless new benchmark evidence invalidates them.

## Locked product decisions

- Clean slate; v1 data/database may be discarded.
- pr0gramm-style authenticated media board; no anonymous posting.
- Invitation-only registration initially; authentication architecture must be extensible later.
- All current v1 features return by end of rewrite; private messages come late.
- Global feed remains chronological by post ID descending.
- Equal-square responsive thumbnails use 100% board width.
- Selected post expands as one full-width row directly below its thumbnail row.
- Same inline behavior on mobile.
- Videos constrained to viewport height; images may span multiple screens.
- Canonical `/post/:id`; direct links reconstruct surrounding feed.
- Arrow keys + J/K navigation.
- Search supports include/exclude tags and structured predicates such as `score:>=100`.
- Users may add tags; only moderators/admins remove tags.
- Comments are nested.
- One visual theme initially.
- Evergreen browsers + Safari only.

## Working architecture

- nginx: TLS, frontend assets, media files, `/api` proxy.
- Go: API/application workflows.
- PostgreSQL: authoritative application state and durable media jobs.
- Redis: ephemeral sessions/cache/rate-limits/wakeups/events; not sole durable copy of critical jobs.
- Rust: media worker.
- Local NVMe: media storage.
- Frontend: TypeScript + Vite; SolidJS is the initial candidate and must earn final selection in M1 benchmark.
- Plain CSS; no general UI kit/animation framework/runtime CSS-in-JS by default.
- Feed pagination: cursor by post ID only, no OFFSET.
- Search: lexer/parser/AST compiled to parameterized SQL.
- Data relationships use immutable numeric IDs, never usernames as foreign keys.
- Preferred job claim model: PostgreSQL transaction + `FOR UPDATE SKIP LOCKED`; Redis may wake workers.

## Performance doctrine

Performance is a primary requirement in every decision. For every code change, ask in order:

1. Can this work/data/round trip be removed?
2. Is there a better architecture/algorithm/data model?
3. Can safe caching/precomputation/streaming/concurrency remove latency/work?
4. Is there a lower-overhead proven primitive/library?
5. Only then: is micro-optimization supported by profiling?

Measure performance-sensitive changes. Do not claim speedups without evidence when measurement is feasible. Feature completion includes relevant performance regression coverage.

## Target host

- Ubuntu 24.04 bare metal
- Intel i7-7700, 4C/8T, max ~4.2 GHz
- 64 GiB RAM
- ~477 GB x2 NVMe RAID1
- measured sequential storage: ~1.7 GB/s write, ~3.3 GB/s read
- 1 Gbit/s full duplex NIC
- shared with GitLab/game/other services

CPU is the primary scarce resource; RAM/disk are relatively abundant and network will generally cap static media before NVMe throughput.

Initial media-worker hypotheses only (must benchmark real pipeline):
- 1 media job concurrently
- video ffmpeg threads ~2-3
- image concurrency ~2
- download concurrency ~4-8
- investigate usable Intel Quick Sync H.264/HEVC separately
- initial PostgreSQL max pool around 8, increase only from measured contention

## Next task — M1 board performance prototype

Create a short-lived branch from current `v2` for M1. Do not touch `master`.

Build a backend-free benchmark/prototype with thousands of fake posts. The prototype must prove the core board architecture before backend work begins:

1. responsive 100%-width equal-square rows
2. deterministic post -> row mapping
3. one inline full-width expanded row below selected thumbnail row
4. selecting within same row swaps content without relocating expanded row
5. selecting across rows relocates expanded row cleanly
6. `/post/:id`, browser Back/Forward, direct-load state
7. arrow + J/K navigation
8. image/video sizing rules
9. long scrolling + incremental loading/retention
10. CSS containment and DOM strategy
11. profiling/benchmark harness and recorded baseline

The shell must react locally to selection without waiting for network. Do not introduce virtualization unless profiling demonstrates that retained DOM is the actual bottleneck.

### M1 framework gate

Start with SolidJS + TypeScript + Vite as the preferred candidate. Keep the prototype dependency surface minimal. If profiling exposes a credible Solid-specific limitation, implement only the smallest comparable alternative needed to make an evidence-based framework decision. Do not benchmark frameworks recreationally.

## Stop conditions / unresolved questions

No blocking product questions currently remain for M1.

Later work still needs evidence/decisions for:
- exact thumbnail target size / responsive column breakpoints (derive and test during M1)
- concrete performance budgets after first prototype baseline
- final password hashing parameters/algorithm on target host
- actual QSV device availability/performance
- final media worker concurrency from real AVIF/video tests
- whether any external consumer requires v1 API compatibility (assume no until identified)

## Session close protocol

Before ending meaningful work, replace/update this file with:

- phase/status
- branch used and relevant commits
- exactly what changed
- tests/checks run and results
- benchmarks with baseline/comparison when relevant
- decisions made and why
- unresolved failures/questions
- one explicit next task

Do not copy long implementation logs here. Link to durable code/tests/docs and keep this file optimized for rapid resume.
