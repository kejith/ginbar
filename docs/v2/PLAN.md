# Ginbar v2 rewrite plan

Status: **M1 complete; M2 complete; M3 in progress**
Last consolidated: 2026-10-03
Integration branch: `v2`
Legacy branch: `master`

For current state and the next task, read [`STATE.md`](STATE.md) first. Stable benchmark results live in [`PERFORMANCE.md`](PERFORMANCE.md).

## 1. Branch safety

- `master` is legacy/v1. Never commit, merge, rebase, force-push, or otherwise modify it for rewrite work.
- `v2` is the permanent rewrite integration branch.
- Normally branch from the current `v2`, validate the smallest coherent slice, then fast-forward reviewed work back into `v2`.
- Legacy code may be consulted as behavioral/algorithmic reference but is not the default implementation base.
- Verify branch/ref before every write.

## 2. Product definition

Ginbar v2 is a clean-slate authenticated pr0gramm-style media board. Existing v1 implementation choices and data are not compatibility constraints unless an external dependency is later identified.

End-state capabilities include:

- accounts with no anonymous posting;
- invitation-only registration initially, with identities designed to add passkeys/OIDC/OAuth/email later without redesigning users;
- chronological post-ID-descending media feed;
- upload and URL import;
- image/video processing, thumbnails, duplicate detection, regeneration;
- post/comment votes and tag behavior;
- tags with user add and moderator/admin removal;
- `sfw`, `nsfp`, `nsfw`, `secret` access semantics;
- real search grammar with include/exclude tags and predicates such as `score:>=100`;
- nested comments;
- profiles, roles, moderation/admin, imports/jobs;
- private messages late in the rewrite.

## 3. Board UX invariant

The board interaction is the primary frontend constraint:

- full-width responsive equal-square thumbnail grid;
- selecting a post inserts exactly one full-width expanded post below that thumbnail row;
- same-row selection replaces content in place;
- cross-row selection moves the expanded row below the new row;
- same interaction model on mobile;
- canonical `/post/:id` URLs and direct-link feed reconstruction;
- coherent Back/Forward history;
- Arrow keys and J/K navigation;
- ordering by post ID descending.

M1 validated SolidJS + TypeScript + Vite, incremental retention, containment, stable row identity, and no initial virtualization. Keep that architecture until new browser evidence justifies a change.

## 4. Performance rules

Performance is a primary requirement. Optimize in this order:

1. eliminate work/data/round trips;
2. improve architecture/algorithm/data layout/API boundaries;
3. cache/precompute/stream/parallelize safely;
4. use lower-overhead proven primitives;
5. micro-optimize only after profiling.

Rules:

- benchmark performance-sensitive changes when practical;
- target-server measurements are authoritative for backend/media work;
- browser timings belong to the browser machine;
- never claim a speedup without comparable measurements;
- API hot SQL must be bounded/indexed for real query shapes and checked with `EXPLAIN (ANALYZE, BUFFERS)`;
- primary feed uses post-ID cursor pagination, never OFFSET;
- opening a post updates the UI immediately and never waits for network before showing the expanded shell;
- avoid whole-board rerenders and unbounded caches;
- known media dimensions should prevent avoidable layout shift;
- background processing must be explicitly concurrency-limited and measured against interactive latency.

Current performance decisions are recorded in `PERFORMANCE.md` rather than duplicated here.

## 5. Preferred architecture

```text
Browser
  |
  v
nginx ----------------------> built frontend assets
  |-------------------------> immutable/static media on local NVMe
  `---- /api --------------> Go API/workflows
                                |
                                +--> PostgreSQL (authoritative app + durable jobs)
                                |
                                `--> optional ephemeral services only when measured
                                        |
                                        v
                                   Rust media worker
```

### Backend

- Go 1.25 + pgx/v5 + standard `net/http` is the accepted M2 stack.
- Keep HTTP handlers thin and domain/workflows behind explicit boundaries.
- PostgreSQL is authoritative.
- Redis is **not** a baseline dependency. Add it only for an ephemeral responsibility demonstrated by measurements or operational need.
- Keep PostgreSQL pools small; current accepted cap is 8 until evidence justifies more.
- Use cancellation/deadlines and bounded concurrency.

### Data/model

- immutable numeric IDs as relational identity;
- never username as foreign key;
- parameterized SQL only;
- explicit release/moderation/processing state;
- indexes based on actual query shapes;
- invitations separate from identity/credentials;
- real search lexer/parser/AST, not ad-hoc string splitting.

### Frontend

- SolidJS + TypeScript + Vite + plain CSS;
- separate route, ephemeral UI, and server state;
- stable row identity and isolated expanded-post updates;
- optimistic updates where correctness permits;
- bounded incremental loading/retention before considering virtualization.

### Media worker

- Rust;
- durable PostgreSQL job ownership with generation fencing;
- filesystem/codec work outside DB transactions;
- deterministic/idempotent side effects for at-least-once processing;
- local NVMe media storage;
- current accepted still-image limit: **one media job at a time, one AVIF encoder thread**;
- worker loop must renew leases, react to cancellation/lost ownership, and shut down gracefully;
- video gets its own explicit thread/concurrency benchmark.

## 6. Target host

Current target assumptions:

- Ubuntu 24.04 bare metal;
- Intel i7-7700, 4 physical / 8 logical CPUs;
- ~64 GiB RAM;
- two ~477 GB NVMe drives in RAID1;
- 1 Gbit/s full-duplex NIC;
- host also runs other services.

Implications:

- CPU is more constrained than RAM/NVMe;
- network generally bottlenecks static media before local NVMe;
- background codecs must remain bounded;
- do not reserve cores, add quotas, or add load admission based on intuition: measure the real runner first.

## 7. Milestones and gates

### M1 — board performance prototype — COMPLETE

Validated responsive grid, inline expanded row, history/navigation, direct links, incremental retention, containment, long scrolling, and frontend framework choice.

Record: [`M1.md`](M1.md).

### M2 — fresh schema + core Go API — COMPLETE

Implemented fresh relational model, users/roles/credentials/invitations foundations, posts/media/tags/comments/votes, search AST, cursor feed, around-post reconstruction, durable media jobs, error model, bounded SQL, and shared-host HTTP/query-plan gate.

Record: [`M2.md`](M2.md).

### M3 — media pipeline — IN PROGRESS

Integrated:

- durable PostgreSQL media-job ownership/retry/recovery;
- upload/URL ingestion + durable enqueue;
- source verification;
- deterministic/generation-fenced DB publication;
- durable no-overwrite local output publication;
- still-image JPEG/PNG/WebP decode, orientation, resize, AVIF canonical + thumbnail;
- crash/idempotency/collision testing;
- real-media and API-coexistence measurements.

Remaining:

- production long-running worker loop/wakeup policy;
- lease renewal during long processing;
- cancellation/lost-ownership handling;
- graceful shutdown/backoff;
- video processing;
- perceptual duplicate detection;
- regeneration;
- progress/status UI;
- production-runner background-load gate.

Gate: crash/restart/lease-loss correctness + real media/video benchmarks + configured background workload must preserve acceptable interactive behavior.

### M4 — connected core product

Auth/invite registration, board connected to API, filters/search, votes, tags, nested comments, profiles.

Gate: end-to-end board performance budgets remain satisfied.

### M5 — moderation/admin/imports

Roles, tag removal/moderation, post/comment moderation, imports, jobs/admin observability.

### M6 — private messages

Implement after core/admin architecture is stable.

### M7 — production hardening

nginx/static caching, process/file limits, DB tuning from evidence, security review, abuse/rate limits, backups/recovery, observability, deployment/update procedure, and performance regression suite.

## 8. Engineering workflow

For every meaningful task:

1. read `STATE.md` first;
2. verify branch/ref and base from current `v2`;
3. inspect relevant code/docs and identify correctness/performance risks;
4. choose the smallest coherent architecture;
5. prefer proven primitives over custom infrastructure;
6. implement;
7. run targeted correctness checks;
8. benchmark/profile performance-sensitive work and compare with baseline;
9. reconsider whether less work or a better boundary wins;
10. update `STATE.md` with changes, evidence, decisions, unresolved issues, and one next task.

Do not leave critical project memory only in chat history.

## 9. Local-agent boundary

Use a local agent when this session lacks browser/DevTools, SSH/server, PostgreSQL, deployment, benchmark, or toolchain access.

Default is read-only/execution-only. A local agent must not modify source, SQL, docs, config, commits, branches, deployments, or persistent state without explicit approval for that specific write.

Target/server evidence should return as one ZIP containing exact SHA/worktree status, commands, stdout/stderr, versions, raw measurements/plans, errors, cleanup proof, and concise findings.

## 10. Definition of done for performance-sensitive work

A feature is not done merely because it works. It must also:

- have a clear ownership/boundary;
- avoid unnecessary allocation/render/query/round-trip work;
- have appropriate indexes/cache/concurrency limits;
- handle cancellation/timeouts/errors correctly;
- test important failure/restart behavior;
- include benchmark/profile evidence or a clear reason measurement is not useful;
- respect established performance budgets;
- leave `STATE.md` ready for the next session.
