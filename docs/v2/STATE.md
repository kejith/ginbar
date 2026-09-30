# Ginbar v2 State / Handoff

Last updated: 2026-09-30
Phase: M1 in progress — backend-free board prototype implemented; real Solid build/profile still required
Integration branch: `v2`
Active implementation branch: `astra/m1-board-prototype`
Legacy branch: `master` (read-only for rewrite work)

## Read this first

This file is the minimal resume point for humans and GPT-6 Astra. Read it before `PLAN.md`. Do not use chat history as project memory.

## Branch status

- `v2` remains the rewrite integration root and was split from `master` at `181fa44d79c7b4a1984c1a35795762dd503b3f77`.
- This session branched from `v2` head `38c515afd2862cb6a3b6a6c677c9b34fa210b662` into `astra/m1-board-prototype`.
- `master` and `v2` were not modified by M1 implementation work.
- Relevant branch commits so far:
  - `d7d6a083d72c61ba9144b4080e616742c989fd90` — initial M1 Solid board prototype
  - `6be8e41b673403590050be2f26542e3f271e1da7` — corrected/cleaned prototype source
  - `75caf6c8d60b12b39bd8fc81776c8a24f422daef` — corrected M1 benchmark notes

## M1 implemented so far

The active frontend entry/config is now a minimal SolidJS + TypeScript + Vite prototype using plain CSS. The stale React entry files and stale pnpm lock/workspace files were removed from the active frontend scaffold. Unrelated legacy backend/worker code was left untouched.

Prototype behavior:

- 10,000 deterministic fake posts ordered by ID descending
- responsive 100%-width equal-square thumbnail rows
- deterministic post-index -> row mapping
- exactly one full-width expanded post directly below the selected thumbnail row
- same-row selection swaps expanded content without relocating the row
- cross-row selection moves the expanded row below the new row
- canonical `/post/:id`, direct-load reconstruction, History API Back/Forward synchronization
- Arrow keys and J/K navigation; Escape closes the expanded post
- images use intrinsic dimensions; videos are constrained by `100svh`
- incremental retention starts at 320 posts and grows in 320-post chunks with `IntersectionObserver`
- row-level `content-visibility: auto` plus layout/paint/style containment
- no virtualization
- optional `?bench` instrumentation exposes `window.__ginbarM1` for selection/retention runs and records next-frame selection latency plus Long Tasks where supported

Durable detail and benchmark procedure: `docs/v2/M1.md`.

## Checks run

- `node --test src/board-model.test.js`: 5/5 passing.
- Source-level TypeScript check passed with local declaration stubs for Solid/Vite because external npm dependencies could not be installed in this execution environment.
- The committed `App.tsx` content blob (`bde8d21f0042c08c8d61502ba63265525ca31f7b`) matches the locally typechecked source exactly.
- `npm ping --registry=https://registry.npmjs.org --fetch-timeout=3000 --fetch-retries=0` fails with `EAI_AGAIN getaddrinfo`; a package-lock-only install also timed out. Therefore a real dependency install, real Solid/Vite type declarations, production build, and Solid runtime browser profile were not run here.

## Measured lower-bound baseline

A framework-free headless-Chromium DOM harness using the same row grouping/containment strategy measured forced-layout relocation cost. This is an architectural lower bound only; it excludes Solid runtime/compiler work and is **not** the M1 framework-gate result.

| Viewport | Retained thumbnails | Same-row p95 | Cross-row p95 |
| --- | ---: | ---: | ---: |
| 1440x900 | 320 | 0.2 ms | 0.3 ms |
| 1440x900 | 5,000 | 0.8 ms | 1.1 ms |
| 390x844 | 320 | 0.2 ms | 0.4 ms |
| 390x844 | 5,000 | 1.7 ms | 1.9 ms |

Interpretation: deterministic row grouping plus CSS containment is not itself an obvious M1-scale bottleneck. These numbers do not justify retaining thousands of DOM nodes indefinitely and do not establish Solid performance.

## Decisions from this slice

- Keep SolidJS as the M1 candidate; do not lock the framework until the real runtime profile exists.
- Keep the dependency surface minimal: Solid + Vite/plugin + TypeScript only for this prototype.
- Use native History, ResizeObserver and IntersectionObserver APIs; no router/store/virtualizer dependency for M1.
- Keep rows as first-class layout units so selection only changes the selected row state and expanded content.
- Do not add virtualization yet. Profile retained DOM first; only add it if measured DOM retention is the bottleneck without breaking inline-row behavior.
- Prototype parameters (176 px minimum thumbnail target, 10-column cap, 320-post load chunk) are test inputs, not final product constants.

## Locked rewrite invariants still in force

- `master` is never a rewrite target.
- Performance is a primary requirement; remove work before optimizing work.
- Global feed ordering is post ID descending; feed pagination later uses ID cursors, never OFFSET.
- Selected posts expand inline below their thumbnail row on desktop and mobile.
- Canonical `/post/:id` and coherent browser history are required.
- Frontend remains plain CSS with no general UI kit/runtime CSS-in-JS by default.
- Preferred backend architecture remains nginx + Go + PostgreSQL + Redis + Rust media worker + local NVMe.
- PostgreSQL is authoritative; Redis is never the sole durable copy of critical jobs.

## Unresolved / M1 gate not yet passed

- Generate a fresh lockfile from the new dependency graph in an environment with npm registry access.
- Run the actual `npm` install, `tsc --noEmit`, Vite production build, and inspect bundle output.
- Run the real Solid prototype in desktop and mobile-class Chromium/Safari-compatible testing.
- Profile direct `/post/:id`, same-row/cross-row selection, Back/Forward, J/K/arrows, long scrolling, DOM count, heap growth, and long tasks at 320 / 2,000 / 5,000 / 10,000 retained posts.
- Decide whether retention needs a cap or virtualization only from those measurements.
- Lock or reject Solid only after that evidence. M2 must not start before M1 passes.

## Single best next task

On a machine with npm registry access, generate the fresh frontend lockfile, run the real Solid/Vite build and typecheck, launch the prototype, then capture desktop (1440x900) and mobile-class (390x844) profiles using `window.__ginbarM1` at 320 / 2,000 / 5,000 / 10,000 retained posts plus direct-route/history/keyboard checks. Use those measurements to decide the Solid framework gate and DOM-retention strategy.
