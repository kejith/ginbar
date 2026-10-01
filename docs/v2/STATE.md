# Ginbar v2 State / Handoff

Last updated: 2026-10-01
Phase: M1 profiling completed; gate remains open due browser-host mismatch and scroll-anchor issue
Integration branch: `v2`
Active implementation branch: `astra/m1-board-prototype`
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the minimal resume point for humans and Astra. Read it before `PLAN.md`. Do not use chat history as project memory.

## Branch status

- `v2` remains the rewrite integration root at base commit `38c515afd2862cb6a3b6a6c677c9b34fa210b662` for this branch.
- `master` and `v2` were not modified by M1 implementation work.
- `astra/m1-board-prototype` is ahead of `v2` and contains the complete current M1 prototype plus docs/runbook.
- Important commits in this session:
  - `446f87cd021fe0aa95bca90f8691af0ceada662e` — stable-row/selective-reactivity board rewrite
  - `a746646400972b77ed565553a4e68dcebcc20af0` — containment/intrinsic-row CSS cleanup
  - `732236c6c46bc83554cf6f59205f295b6d5afab3` — one-command frontend validation
  - `56b5cba524fb35b058a3b33289e75ce3292b3eb0` — target-hardware M1 runbook
  - `594e5003952d9c32a0e9c46ee006a2a17ed3fc5e` — updated M1 architecture/benchmark notes

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
Target-hardware procedure: `docs/v2/M1_RUN.md`.

## Performance-oriented decisions already implemented

- Rows are keyed by stable absolute numeric row indexes. Retention growth/prepend should add rows without rebuilding existing rows.
- Selection membership uses Solid `createSelector`; retained thumbnails/rows should not all react to every selected-ID change.
- Far history/route jumps recenter a bounded window rather than constructing a huge contiguous prefix.
- Direct links reconstruct roughly one 320-post window around the selected item, then extend in either direction on demand.
- No router, global store, virtualizer, UI kit, animation framework, or runtime CSS-in-JS was added.
- Sticky-header backdrop blur was removed to avoid needless scroll repaint work in the benchmark.
- Virtualization remains deliberately absent until retained-DOM profiling proves it is needed.

## Pre-hardware checks completed in the original local environment

- `node --test src/board-model.test.js`: 8/8 passing.
- Pure tests cover deterministic data, responsive columns, row mapping, index-range -> row-range mapping, bounded deep-link windows, bidirectional range extension, strict `/post/:id` parsing, and navigation boundary clamping.
- Source-level TypeScript check passes with local Solid declaration stubs using TypeScript 5.8.3.
- Local runtime used for those checks: Node v22.16.0.
- Committed content blobs exactly match the locally checked files:
  - `App.tsx`: `3378d081fe3b0d928bd9ef48699b3256c0f93dfa`
  - `board-model.js`: `f20130d4741a75759512b0d95b4f67b86dabe1a1`
  - `board-model.test.js`: `3a8d66654e2349cb48a2d7de573f96e008c0a04a`
  - `styles.css`: `d185eda4e4093aea54cb798fe270d60385c72fbe`
- External npm access is unavailable here: `npm ping --registry=https://registry.npmjs.org --fetch-timeout=3000 --fetch-retries=0` fails with `EAI_AGAIN getaddrinfo`.
- Therefore real dependency installation, real Solid/Vite declaration checking, production build, and real Solid runtime profiling were not possible here.

## Existing lower-bound measurement

A framework-free headless-Chromium DOM harness using the same row/containment concept measured forced-layout relocation cost. This excludes Solid runtime/compiler work and is not the framework-gate result.

| Viewport | Retained thumbnails | Same-row p95 | Cross-row p95 |
| --- | ---: | ---: | ---: |
| 1440x900 | 320 | 0.2 ms | 0.3 ms |
| 1440x900 | 5,000 | 0.8 ms | 1.1 ms |
| 390x844 | 320 | 0.2 ms | 0.4 ms |
| 390x844 | 5,000 | 1.7 ms | 1.9 ms |

Interpretation: row layout/containment alone is not an obvious M1-scale bottleneck. Do not use these numbers to accept Solid or to justify indefinite DOM retention.

## M1 hardware profiling update (2026-10-01)

- Target tested: `astra/m1-board-prototype` at `da0885f724d662333af45dc307c1d501135da266`; results-only branch: `astra/m1-hardware-results`.
- Remote Node 22 install, `npm run validate`, standalone test/typecheck/build, production bundle sizing, desktop/mobile benchmark matrices, direct routes, history, keyboard navigation, and 10,000-post long scroll all completed. Detailed data and limitations are in `docs/v2/M1_HARDWARE_RESULTS.md` and `docs/v2/M1_HARDWARE_RESULTS.json`.
- The browser ran on Windows i5-14600KF, not the remote i7-7700; therefore Solid's target-hardware gate is **not decided** despite low measured selection sync p95.
- Prepending newer rows from `/post/5000` caused a large viewport/scroll-position jump at both viewports. This is documented and was not fixed during profiling.
- At 10,000 posts the board retained about 32.5k desktop / 40k mobile-viewport DOM nodes. No retention-cap or virtualization decision is made from this run.
- Wallium was restored and verified active. No application source changed; `master`, `v2`, and `main` were not modified.

## M1 gate still outstanding

- Resolve the browser-host limitation before treating the selection and scroll timings as target-hardware evidence.
- Investigate the reproducible prepend scroll-anchor jump in a separate implementation task, then rerun the M1 scroll and profile checks.
- Review Solid acceptance and any retention/virtualization decision only after the target-relevant browser profile and scroll correctness are established.

Do not start M2 or merge the M1 implementation branch into `v2` until the gate is reviewed.

## Single best next task

Reproduce the `/post/5000` prepend-anchor jump, address it in a separate M1 implementation task, and rerun the bidirectional scroll test before making a Solid or retention decision. Keep the hardware-result branch documentation-only.
