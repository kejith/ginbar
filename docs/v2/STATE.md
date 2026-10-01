# Ginbar v2 State / Handoff

Last updated: 2026-10-01
Phase: M1 prepend scroll-anchor fix implemented; local client/browser validation still required before gate review
Integration branch: `v2`
Base M1 implementation branch: `astra/m1-board-prototype`
Active fix branch: `astra/m1-scroll-anchor`
Hardware/client results branch: `astra/m1-hardware-results`
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the minimal resume point for humans and Astra. Read it before `PLAN.md`. Do not use chat history as project memory.

## Branch status

- `v2` remains the rewrite integration root and was not modified.
- `master` remains untouched by rewrite work.
- `astra/m1-board-prototype` remains at `da0885f724d662333af45dc307c1d501135da266` and is the base for this fix.
- `astra/m1-scroll-anchor` was created directly from that exact commit.
- `astra/m1-hardware-results` remains documentation/results-only and contains the first profiling run plus the benchmark execution rule.

## First profiling result

The 2026-10-01 profiling run validated the existing M1 prototype without changing application source:

- real `npm install`, `npm run validate`, tests, typecheck, and production build passed;
- production JS: 26,539 B raw / 10,122 B gzip;
- production CSS: 3,293 B raw / 1,402 B gzip;
- client-browser selection sync p95 stayed roughly 0.4-0.5 ms from 320 through 10,000 retained posts;
- 10,000 retained posts produced about 32.5k DOM nodes at 1440x900 and about 40k at 390x844;
- direct routes, bounded deep links, same/cross-row selection, Back/Forward, Arrow keys, J/K, Escape, and full 10,000-post traversal passed;
- reproducible blocker: prepending newer posts from a centered deep link such as `/post/5000` moved the viewport far away from the previously visible content.

Detailed first-run results are on `astra/m1-hardware-results` in `docs/v2/M1_HARDWARE_RESULTS.md` and `.json`.

## Benchmark execution rule

Frontend and backend performance evidence are intentionally separated:

- frontend/browser benchmarks belong to the client machine executing JavaScript/layout/paint;
- the remote target server serving frontend assets does not make browser timings target-server timings;
- future target-server performance benchmarks are authoritative only for backend/server-side implementation such as Go API, PostgreSQL, Redis, Rust worker, nginx/static-serving overhead, concurrency, and server-side contention;
- do not block frontend framework decisions on absence of a browser running on the server; record client/browser hardware explicitly.

## Scroll-anchor fix implemented on `astra/m1-scroll-anchor`

The prepend path now uses explicit viewport anchoring instead of relying on browser native anchoring:

1. capture the row currently under the viewport immediately below the sticky header before a top-edge range extension;
2. fall back to the selected row if no visible row can be sampled;
3. prepend the next 320-post chunk using the existing stable absolute row keys;
4. on the next animation frame, measure movement of that same DOM row;
5. scroll by exactly the measured delta before paint;
6. set `overflow-anchor: none` on the board so native browser anchoring does not compete with the explicit correction;
7. coalesce overlapping prepend requests until the two-frame correction/probe completes.

The benchmark API now exposes `await window.__ginbarM1.prepend()` and returns `beforeTop`, `shiftedTop`, `afterTop`, the applied correction, and before/after logical ranges. This provides a deterministic regression probe for the previously timing-sensitive sentinel behavior.

## Current branch commits

- `bf7b82a7a4407d219692c85669986b5a01c5ab61` — explicit viewport-anchor compensation and benchmark probe
- `d72ac8a3fc9ea325078a246fa836403b2d65b34b` — disable competing native board scroll anchoring
- `fe0ac4a258532d1a35fae6636bbeb00d44ff9169` — document anchor design and rerun procedure

## Checks completed in this session

- The exact committed `App.tsx` blob is `c3bdf9527210a69e3d48441a4222bb192a931114` and matches the locally checked source byte-for-byte.
- TypeScript 5.8.3 source checking passed using local Solid declaration stubs; no source-level type errors were found.
- A framework-free Chromium forced-layout harness with native anchoring disabled inserted 10,000 px above the visible anchor; applying `shiftedTop - beforeTop` via `scrollBy` restored the same row to its original viewport position with 0 px residual error.
- `board-model.js` and its existing 8/8 tests were not changed by this fix.
- External npm access remains unavailable in the Astra execution environment, so the real dependency-backed Solid/Vite build of this new branch must be rerun locally.

## M1 gate status

M1 is not yet passed. The implementation-side blocker has a fix, but it needs client/browser confirmation through the real Solid build.

Required before gate review:

- run `npm run validate` on `astra/m1-scroll-anchor` with real dependencies;
- at both 1440x900 and 390x844, open `/post/5000` and run `await window.__ginbarM1.prepend()`; `afterTop - beforeTop` should remain near zero and invariants must pass;
- rerun wheel-driven bidirectional scrolling around `/post/5000` to exercise the real top-sentinel path;
- rerun a representative 10,000-post selection/scroll profile to ensure no new Long Tasks or selection regression;
- then review Solid acceptance and retention/virtualization based on the corrected client-browser evidence.

Do not begin M2 or merge into `v2` until this M1 gate review is complete.

## Single best next task

On the local client/browser environment with real dependencies, validate `astra/m1-scroll-anchor`, run the deterministic `/post/5000` `window.__ginbarM1.prepend()` probe at desktop and mobile-class viewports, then repeat bidirectional wheel scrolling and one representative 10,000-post profile. If anchoring remains stable without a performance regression, use that evidence to make the M1 Solid and retention decisions.
