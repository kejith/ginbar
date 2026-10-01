# Ginbar v2 State / Handoff

Last updated: 2026-10-01
Phase: M1 frontend architecture gate passed; integration into `v2` waits only on committing the validated frontend lockfile
Integration branch: `v2`
Base M1 implementation branch: `astra/m1-board-prototype`
Validated M1 branch: `astra/m1-scroll-anchor`
Hardware/client results branch: `astra/m1-hardware-results`
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the minimal resume point for humans and Astra. Read it before `PLAN.md`. Do not use chat history as project memory.

## Branch status

- `v2` remains the rewrite integration root and is still unchanged at `38c515afd2862cb6a3b6a6c677c9b34fa210b662`.
- `master` remains untouched by rewrite work.
- `astra/m1-board-prototype` remains at `da0885f724d662333af45dc307c1d501135da266`.
- `astra/m1-scroll-anchor` was created directly from that commit and contains the validated prepend-anchor fix plus M1 gate documentation.
- `astra/m1-hardware-results` remains documentation/results-only and contains the first profiling run plus the benchmark execution rule.

## M1 implementation

The frontend is a minimal SolidJS + TypeScript + Vite prototype using plain CSS and native browser APIs.

Implemented and validated:

- 10,000 deterministic fake posts ordered by ID descending
- responsive 100%-width equal-square thumbnail rows
- deterministic absolute post-index -> row mapping
- exactly one inline full-width expanded post beneath the selected row
- same-row replacement and cross-row relocation
- canonical `/post/:id`, direct-load reconstruction, History API Back/Forward
- Arrow keys and J/K navigation; Escape closes the expanded post
- intrinsic image sizing and viewport-bounded video sizing
- bidirectional incremental loading using top/bottom `IntersectionObserver` sentinels
- bounded deep-link windows rather than retaining every newer post to reach an old post
- stable absolute row keys and Solid `createSelector` for targeted selection reactivity
- row-level `content-visibility`, containment, and intrinsic-size placeholders
- explicit viewport anchoring when newer rows are prepended
- benchmark/invariant API via `window.__ginbarM1`
- no virtualization

Durable architecture detail: `docs/v2/M1.md`.
First profiling results: `astra/m1-hardware-results` -> `docs/v2/M1_HARDWARE_RESULTS.md` and `.json`.

## Benchmark execution rule

Frontend and backend performance evidence are intentionally separated:

- frontend/browser benchmarks belong to the client machine executing JavaScript/layout/paint;
- the remote target server serving frontend assets does not make browser timings target-server timings;
- future target-server performance benchmarks are authoritative only for backend/server-side implementation such as Go API, PostgreSQL, Redis, Rust worker, nginx/static-serving overhead, concurrency, and server-side contention;
- do not block frontend framework decisions on absence of a browser running on the server; record client/browser hardware explicitly.

## First profiling run

The 2026-10-01 client/browser run of `astra/m1-board-prototype` at `da0885f724d662333af45dc307c1d501135da266` established:

- real `npm install`, `npm run validate`, standalone tests/typecheck/build all passed;
- production JS: 26,539 B raw / 10,122 B gzip;
- production CSS: 3,293 B raw / 1,402 B gzip;
- selection sync p95 stayed roughly 0.4-0.5 ms from 320 through 10,000 retained posts;
- 10,000 retained posts produced about 32.5k DOM nodes at 1440x900 and about 40k at 390x844;
- direct routes, bounded deep links, same/cross-row selection, Back/Forward, Arrow keys, J/K, Escape, and full 10,000-post traversal passed;
- the only reproducible M1 correctness blocker found was a large viewport jump when newer rows were prepended from a centered deep link.

## Prepend-anchor fix

`astra/m1-scroll-anchor` fixes that defect by explicitly preserving a visible row across a top-edge range extension:

1. capture the visible row immediately below the sticky header;
2. prepend using stable absolute row keys;
3. measure that same row on the next animation frame;
4. scroll by its measured viewport delta before paint;
5. disable native board `overflow-anchor` so browser anchoring does not compete;
6. coalesce overlapping prepend requests until correction completes.

The benchmark API exposes `await window.__ginbarM1.prepend()` for deterministic regression checking.

Implementation commits:

- `bf7b82a7a4407d219692c85669986b5a01c5ab61` — explicit viewport-anchor compensation and benchmark probe
- `d72ac8a3fc9ea325078a246fa836403b2d65b34b` — disable competing native board scroll anchoring
- `fe0ac4a258532d1a35fae6636bbeb00d44ff9169` — document anchor design and rerun procedure
- `fae11b007b261422a2c753d3c77a6176ceacf97f` — handoff after source-level validation

## Local validation of corrected branch

The validated source commit was `fae11b007b261422a2c753d3c77a6176ceacf97f` with merge base `da0885f724d662333af45dc307c1d501135da266`.

On the local Windows client/browser environment:

- `npm install` passed;
- `npm run validate` passed, including all 8 tests, real TypeScript checking, and production Vite build;
- `await window.__ginbarM1.prepend()` preserved the selected row with 0 px residual viewport movement at both 1440x900 and 390x844, with invariants passing;
- wheel scrolling extended the newer logical range edge from 4,520 to 3,240 and the older edge from 5,480 to 5,800, with invariants passing;
- representative 10,000-post desktop profile: same-row sync p95 0.3 ms, cross-row sync p95 0.4 ms, cross-row frame p95 17.1 ms, 32,532 DOM nodes, zero Long Tasks during the matrix.

These are client/browser measurements, not remote i7-7700 frontend timings.

## M1 frontend gate decision

### SolidJS: accepted

Solid remains the v2 frontend framework candidate and is now accepted by the M1 gate.

Reasoning from measurements:

- selection sync cost remained effectively flat through 10,000 retained posts;
- corrected 10,000-post same/cross-row selection remained below 0.5 ms sync p95 in the measured client run;
- stable row identity, bounded direct-link reconstruction, route/history semantics, keyboard navigation, and bidirectional scrolling are correct;
- no framework-specific architectural bottleneck appeared in the measured matrix/traces.

This decision is scoped to the M1 board architecture; future product screens still require normal profiling.

### Retention / virtualization: keep simple incremental retention

Do not add virtualization now.

At 10,000 synthetic posts the retained DOM is large (about 32.5k nodes desktop and about 40k at the tested mobile-class width), but the evidence does not show retained DOM as the bottleneck for M1 selection or scrolling after the anchor fix. Continue with incremental loading + containment.

If later real-product profiling shows memory or scrolling pressure, evaluate bounded retention before full virtualization so inline expanded-row semantics and browser history remain simple.

## Remaining integration item

The successful local `npm install` generated an untracked `src/frontend/package-lock.json` in the anchor worktree. It has not been pushed, so the exact dependency graph used for the successful validation is not yet reproducible from Git.

Do not fast-forward `v2` or begin M2 until the generated validated lockfile is committed to `astra/m1-scroll-anchor` and the local command below passes against it:

```bash
npm ci && npm run validate
```

Once that passes, `astra/m1-scroll-anchor` is a clean descendant of `v2` with no competing integration changes and can be fast-forwarded into `v2`.

## Single best next task

Commit the generated `src/frontend/package-lock.json` from the validated local `astra/m1-scroll-anchor` worktree, run `npm ci && npm run validate`, and push that commit. Then fast-forward the completed M1 branch into `v2` and start M2 from current `v2`.
