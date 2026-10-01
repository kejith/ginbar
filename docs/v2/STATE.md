# Ginbar v2 State / Handoff

Last updated: 2026-10-01
Phase: M1 complete; M2 may begin from current `v2`
Integration branch: `v2`
Completed M1 branch: `astra/m1-scroll-anchor`
Hardware/client results branch: `astra/m1-hardware-results`
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the minimal resume point for humans and Astra. Read it before `PLAN.md`. Do not use chat history as project memory.

## Branch status

- `master` remains untouched by rewrite work.
- M1 started from `v2` at `38c515afd2862cb6a3b6a6c677c9b34fa210b662`.
- `astra/m1-board-prototype` produced the initial validated board prototype at `da0885f724d662333af45dc307c1d501135da266`.
- `astra/m1-scroll-anchor` contains the completed M1 implementation, prepend-anchor fix, M1 decision docs, and committed npm lockfile.
- The validated lockfile commit is `82de7b2f90735281b00d7c23ccc3d02e714389de`.
- This completed M1 history is a clean fast-forward descendant of `v2`; no competing `v2` commits were present at integration time.

## M1 completion

M1 is complete and the frontend framework gate is passed.

Validated behavior:

- 10,000 deterministic fake posts ordered by ID descending
- responsive 100%-width equal-square grid
- deterministic absolute row mapping
- exactly one inline full-width expanded post below the selected thumbnail row
- same-row replacement and cross-row relocation
- canonical `/post/:id`, direct links, Back/Forward
- Arrow keys and J/K navigation; Escape close
- intrinsic media sizing
- bidirectional incremental loading
- bounded deep-link reconstruction
- stable absolute row identity
- targeted Solid selection reactivity using `createSelector`
- row-level containment and `content-visibility`
- explicit prepend viewport anchoring
- benchmark/invariant API via `window.__ginbarM1`
- no virtualization

Durable M1 architecture/results summary: `docs/v2/M1.md`.
Detailed first profiling results remain on `astra/m1-hardware-results` in `docs/v2/M1_HARDWARE_RESULTS.md` and `.json`.

## M1 validation evidence

Initial client/browser profiling established:

- production JS 26,539 B raw / 10,122 B gzip
- production CSS 3,293 B raw / 1,402 B gzip
- selection sync p95 roughly 0.4-0.5 ms from 320 through 10,000 retained posts
- about 32.5k DOM nodes at 10,000 posts on 1440x900 and about 40k at 390x844
- direct routes, bounded deep links, same/cross-row selection, history, keyboard navigation, and full traversal passed

That run found one reproducible correctness issue: prepending newer rows from a centered deep link caused a large viewport jump.

The corrected `astra/m1-scroll-anchor` implementation was then validated locally:

- `npm install` passed
- `npm run validate` passed: all 8 tests, real TypeScript checking, production Vite build
- deterministic `window.__ginbarM1.prepend()` preserved the selected row with 0 px residual movement at both 1440x900 and 390x844
- wheel scrolling extended the newer logical range from 4,520 to 3,240 and the older range from 5,480 to 5,800 with invariants passing
- representative 10,000-post desktop profile: same-row sync p95 0.3 ms, cross-row sync p95 0.4 ms, cross-row frame p95 17.1 ms, 32,532 DOM nodes, zero Long Tasks during the matrix

The exact dependency graph is now committed in `src/frontend/package-lock.json`. Against that committed lockfile:

```bash
npm ci
npm run validate
```

passed successfully, including all 8 tests, typecheck, and production build.

## M1 decisions

### SolidJS

Accepted for v2.

Selection cost remained effectively flat through 10,000 retained posts in measured client runs, the corrected board remained below 0.5 ms sync p95 for same/cross-row selection, and no Solid-specific architectural bottleneck appeared.

### Retention / virtualization

Keep incremental retention + CSS containment. Do not add virtualization now.

The 10,000-post retained DOM is large, but it was not demonstrated as the M1 interaction bottleneck. If real product workloads later show memory or scroll pressure, evaluate bounded retention before full virtualization.

## Benchmark execution rule

Frontend and backend performance evidence are intentionally separated:

- frontend/browser performance belongs to the client machine executing JavaScript/layout/paint;
- a remote server serving frontend assets does not make browser timings server-hardware timings;
- future target-server benchmarks are authoritative for backend/server-side implementation only: Go API, PostgreSQL, Redis, Rust worker, nginx/static serving, concurrency, and server contention as applicable.

## M2 scope

M2 is the fresh v2 schema + core Go API milestone:

- fresh migrations/schema
- users/roles/invitations/credentials
- posts/media metadata/filters/release state
- tags and tag-search AST
- cursor feed and around-post/deep-link endpoints
- comments/votes foundations
- durable media-job table/state machine
- strict API contracts and error model

M2 gate: benchmark representative feed/search/post endpoints and inspect important PostgreSQL query plans with `EXPLAIN (ANALYZE, BUFFERS)`.

## Single best next task

Create a short-lived M2 branch from the current `v2` head and design/implement the smallest coherent foundation for the fresh PostgreSQL schema plus thin Go API skeleton. Start with immutable numeric identities, users/credentials/invitations/roles, posts/media state, and the ID-descending cursor-feed query shape; add migrations and tests before expanding into search/comments/votes/media jobs. Benchmark the first real feed endpoint/query on the target server once it exists.