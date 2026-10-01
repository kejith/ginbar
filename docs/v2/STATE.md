# Ginbar v2 State / Handoff

Last updated: 2026-10-01
Phase: M1 profiling completed; gate remains open due prepend scroll-anchor correctness issue
Integration branch: `v2`
Active implementation branch: `astra/m1-board-prototype`
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the minimal resume point for humans and Astra. Read it before `PLAN.md`. Do not use chat history as project memory.

## Branch status

- `v2` remains the rewrite integration root at base commit `38c515afd2862cb6a3b6a6c677c9b34fa210b662` for this branch.
- `master` and `v2` were not modified by M1 implementation work.
- `astra/m1-board-prototype` contains the current M1 implementation.
- `astra/m1-hardware-results` is documentation/results-only and must remain free of application source changes.

## M1 implementation status

The active frontend is a minimal SolidJS + TypeScript + Vite prototype using plain CSS and native browser APIs.

Implemented:

- 10,000 deterministic fake posts ordered by ID descending
- responsive 100%-width equal-square thumbnail rows
- deterministic absolute post-index -> row mapping
- exactly one inline full-width expanded post beneath the selected row
- same-row selection swaps expanded content in place; cross-row selection relocates the expanded row
- canonical `/post/:id`, direct-load reconstruction, History API Back/Forward synchronization
- Arrow keys and J/K navigation; Escape closes the expanded post
- intrinsic image sizing and viewport-bounded video sizing
- bidirectional incremental loading using top/bottom `IntersectionObserver` sentinels
- bounded direct-link windows around old posts instead of retaining every newer post to reach the selected ID
- row-level `content-visibility`, containment, and measured intrinsic-size placeholders
- benchmark/invariant API via `window.__ginbarM1`
- no virtualization

Durable architecture/benchmark detail: `docs/v2/M1.md`.
Profiling procedure: `docs/v2/M1_RUN.md`.
Profiling results: `docs/v2/M1_HARDWARE_RESULTS.md` and `docs/v2/M1_HARDWARE_RESULTS.json`.

## Performance-oriented decisions already implemented

- Rows are keyed by stable absolute numeric row indexes. Retention growth/prepend should add rows without rebuilding existing rows.
- Selection membership uses Solid `createSelector`; retained thumbnails/rows should not all react to every selected-ID change.
- Far history/route jumps recenter a bounded window rather than constructing a huge contiguous prefix.
- Direct links reconstruct roughly one 320-post window around the selected item, then extend in either direction on demand.
- No router, global store, virtualizer, UI kit, animation framework, or runtime CSS-in-JS was added.
- Sticky-header backdrop blur was removed to avoid needless scroll repaint work in the benchmark.
- Virtualization remains deliberately absent until retained-DOM profiling proves it is needed.

## M1 profiling results (2026-10-01)

Target tested: `astra/m1-board-prototype` at `da0885f724d662333af45dc307c1d501135da266`.

- Real `npm install`, `npm run validate`, standalone tests/typecheck/build, and production bundle sizing all passed.
- Production bundle: JS 26,539 B raw / 10,122 B gzip; CSS 3,293 B raw / 1,402 B gzip.
- Browser profiling ran on the local Windows i5-14600KF client using Chromium/Electron while the remote i7-7700 server served the production frontend.
- Selection sync p95 stayed roughly 0.4-0.5 ms from 320 through 10,000 retained posts in that client/browser run.
- At 10,000 retained posts the board held about 32.5k DOM nodes at 1440x900 and 40.0k at 390x844.
- Direct routes, same/cross-row selection, Back/Forward, Arrow keys, J/K, Escape, bounded deep-link windows, and full 10,000-post traversal passed functional checks.
- Reproducible defect: prepending newer rows from `/post/5000` causes a large viewport/scroll-position jump. This remains an M1 correctness blocker.
- Wallium was restored after the run. No application source changed; `master`, `v2`, and `main` were not modified.

## Benchmark execution rule

Frontend and backend performance evidence are intentionally separated:

- **Frontend/browser benchmarks run on the client machine that executes the browser.** Do not attribute JavaScript, layout, paint, DOM, heap, input, or scroll timings to the remote server merely because it served the assets.
- **For future server benchmarks, only backend/server-side implementation may be assumed to execute on and be validated against the remote target hardware.** This includes Go API, PostgreSQL, Redis, Rust worker, nginx/static-serving overhead, server concurrency, and server-side resource contention as applicable.
- The remote server may serve frontend assets for integration/correctness testing, but that does not make browser measurements server-hardware measurements.
- Do not block frontend framework decisions on absence of a browser running on the server. Use explicit client/browser test hardware and record it with the results.

## M1 gate status

The previous browser-host mismatch is no longer considered an M1 blocker: frontend code executes on the client, and its benchmark hardware must be reported as client hardware rather than server hardware.

M1 remains open because:

- the `/post/5000` prepend-anchor jump is a reproducible interaction correctness issue;
- bidirectional scrolling must be rerun after that fix;
- Solid acceptance and retention/virtualization decisions should be reviewed against the existing client-browser measurements plus the corrected scroll behavior.

Do not start M2 or merge the M1 implementation branch into `v2` until the M1 gate is reviewed.

## Single best next task

On a short-lived implementation branch from current `astra/m1-board-prototype`, reproduce and fix the `/post/5000` prepend-anchor jump with the smallest scroll-anchoring solution, then rerun the local client/browser bidirectional-scroll and selection checks. Do not use the server as evidence for frontend execution performance; reserve target-server benchmarking for backend work later.
