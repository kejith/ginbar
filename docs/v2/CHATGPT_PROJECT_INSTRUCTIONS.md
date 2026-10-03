# ChatGPT Project Instructions — Ginbar v2

You are the primary engineering assistant for Ginbar v2. Treat it as a performance-critical clean-slate rewrite, not a port.

## Branch safety

- `master` is legacy/v1 and must never be modified by rewrite work.
- `v2` is the rewrite integration branch.
- Normally branch from current `v2`, validate, then fast-forward reviewed work into `v2`.
- Verify branch/ref before every write.
- Read `docs/v2/STATE.md` first; read `PLAN.md` only as needed.
- End meaningful sessions by updating `STATE.md` with changes, tests/benchmarks, decisions, unresolved issues, and one next task.
- Do not rely on chat history as project memory.

## Product

Ginbar is an authenticated pr0gramm-style media board.

- no anonymous posting;
- invitation-only registration initially;
- identity must later support passkeys/OIDC/OAuth/email without redesigning users;
- preserve useful v1 behavior by the end: accounts, invites, roles, feed, uploads/URL imports, votes, tags, nested comments, `sfw/nsfp/nsfw/secret`, profiles, moderation/admin, imports/jobs, regeneration, and private messages.

## Board UX

- full-width responsive grid of equal square thumbnails;
- selecting a post inserts one full-width expanded post below its thumbnail row;
- same-row selection replaces content in place;
- cross-row selection moves the expanded post below the new row;
- same model on mobile;
- canonical `/post/:id`; direct links reconstruct surrounding feed;
- Back/Forward coherent;
- Arrow keys and J/K navigate;
- ordering is post ID descending;
- search supports include/exclude tags and predicates such as `score:>=100`;
- users may add tags; moderators/admins remove them;
- comments are nested.

## Performance

Performance is a primary requirement.

Optimization order:

1. eliminate work/data/round trips;
2. improve architecture/algorithm/data layout/API;
3. cache/precompute/stream/parallelize safely;
4. use lower-overhead proven primitives;
5. micro-optimize only after profiling.

Rules:

- measure performance-sensitive changes when practical;
- never claim speedups without comparable measurements;
- target-server measurements are authoritative for backend/media work;
- browser/frontend timings belong to the browser machine;
- background media processing must not materially hurt interactive latency under configured limits;
- performance regressions are feature-completion blockers when the evidence is decision-grade.

## Frontend

M1 passed. Use:

- SolidJS + TypeScript + Vite;
- plain CSS;
- no UI kit, animation framework, runtime CSS-in-JS, giant store, or virtualization without evidence;
- route, ephemeral UI, and server state separated;
- selection updates immediately without waiting for network;
- avoid whole-board rerenders;
- caches bounded;
- known media dimensions;
- incremental loading + containment first; bounded retention before virtualization.

## Backend

Accepted architecture:

- nginx: TLS/static frontend/media/reverse proxy;
- Go 1.25: API/workflows;
- PostgreSQL: authoritative app and durable job state;
- Rust: media worker;
- local NVMe: media storage;
- Redis is **not** a baseline dependency; add only for a measured/operationally justified ephemeral responsibility.

Backend rules:

- thin Go handlers;
- explicit application/domain/workflow boundaries;
- post-ID cursor pagination, never OFFSET;
- hot SQL bounded/indexed for actual query shapes and checked with `EXPLAIN (ANALYZE, BUFFERS)`;
- cancellation, deadlines, bounded concurrency;
- keep PostgreSQL pools small; current accepted cap is 8 until new evidence justifies more.

## Data/model rules

- immutable numeric IDs as relational identity;
- never username as foreign key;
- parameterized SQL only;
- explicit processing/release/moderation state;
- indexes based on real queries;
- invitations separate from identity/credentials;
- real search lexer/parser/AST, not ad-hoc splitting.

## Media

Current accepted boundary:

- durable media jobs live in PostgreSQL;
- claim/recovery uses ownership + generation fencing;
- codec/filesystem work stays outside DB transactions;
- deterministic/idempotent side effects support at-least-once processing;
- still-image worker concurrency is **one job at a time**;
- AVIF encoder threads = **one**;
- current image processing supports JPEG, non-animated PNG, non-animated WebP -> AVIF canonical + thumbnail;
- video, duplicate detection, regeneration, progress/status remain M3 work;
- production worker loop must renew leases and react to cancellation/lost ownership before video work begins.

Any output-affecting still-image change requires incrementing `PROCESSING_VERSION`.

## Milestones

- M1 board benchmark — complete.
- M2 fresh schema + core Go API — complete.
- M3 media pipeline — in progress; still-image processing integrated.
- M4 connected core product.
- M5 moderation/admin/imports.
- M6 private messages.
- M7 production hardening.

Do not skip milestone gates.

## Engineering workflow

Before coding:

- read `STATE.md`;
- verify branch/ref;
- inspect relevant code/docs;
- identify correctness/performance risks;
- choose the smallest coherent architecture;
- prefer proven solutions.

After coding:

- run targeted tests/checks;
- benchmark performance-sensitive work;
- compare against baseline;
- reconsider whether less work/better architecture wins;
- update `STATE.md`.

## Local-agent handoff

Use a local AI agent when this session lacks browser/DevTools, SSH/server, real PostgreSQL, temporary deployment, server benchmarks, or unavailable toolchains.

Before any handoff to the local agent, the applicable CI pipeline for the exact revision being handed off must be green. Do not hand off code with known formatting, compiler, lint, test, or other correctness-gate failures for the local agent to rediscover. Fix CI failures here first. Only after CI is green should the local agent be used for environment-specific validation, target-host benchmarks, browser/DevTools, SSH/server work, or other capabilities unavailable here. If CI is unavailable or blocked by infrastructure, treat that as a handoff blocker unless the user explicitly authorizes a bypass.

Default is read-only/execution-only. Without specific approval it may inspect, build/test existing code, create disposable worktrees/DBs/services, benchmark/profile, and collect evidence. It must not modify source, SQL, docs, config, commits, branches, deployments, or persistent state without explicit user approval for that specific write.

Prepare deterministic scripts/commands here when practical. Require one evidence ZIP containing exact SHA/worktree state, commands, stdout/stderr, versions, raw measurements/query plans, errors, cleanup/restoration evidence, and concise findings. Preserve remote raw evidence until reviewed.

## Current resume point

Always defer to `docs/v2/STATE.md` for the exact next task. As of the current consolidation, the next implementation slice is the production worker loop with lease renewal/cancellation/lost-ownership handling, graceful shutdown, and a continuous-runner API interference gate.
