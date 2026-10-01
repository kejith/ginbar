# M1 Hardware Profile Results

Run date: 2026-10-01 (CEST)
Target branch: `astra/m1-board-prototype`
Target commit: `da0885f724d662333af45dc307c1d501135da266`
Results branch: `astra/m1-hardware-results`, based directly on the target commit

## Outcome

Profiling-only run; no frontend source was changed. The selection benchmark is promising on the local Windows browser, but this does **not** pass the target-hardware framework gate: JavaScript ran on the Windows i5-14600KF, not the remote i7-7700. Scrolling also exposed a reproducible prepend-anchor jump at both viewports. Solid acceptance and any retention/virtualization decision remain open.

Full machine-readable benchmark outputs are in [M1_HARDWARE_RESULTS.json](M1_HARDWARE_RESULTS.json). Screenshots: [desktop at 10,000 posts](m1-desktop-10000.png) and [mobile at 10,000 posts](m1-mobile-10000.png).

## Environment

- Tested source: `origin/astra/m1-board-prototype` at `da0885f724d662333af45dc307c1d501135da266`.
- Remote target: Ubuntu 24.04.3, kernel `6.8.0-88-generic`, Intel Core i7-7700, 4 cores / 8 threads, 62 GiB RAM (44 GiB available at inspection).
- Build container: Node `v22.23.3`, npm `10.9.9`, `node:22-alpine`.
- Browser host: Windows version 10.0.26200 (Get-ComputerInfo product label: Windows 10 Home), Intel Core i5-14600KF, 20 logical processors, 32 GiB RAM. Browser user agent was Code 1.138.0 / Electron 42.10.0 / Chromium 148.0.7778.280.
- Browser viewports: 1440x900 and 390x844. The mobile viewport was emulated on the same Windows desktop CPU; it was not a mobile device.
- The production Vite preview ran in a dedicated remote container on `127.0.0.1:41731`, reached via the Windows SSH forward at `127.0.0.1:41732`. `/` and direct `/post/23` returned HTTP 200. The preview was removed after profiling.

**Interpretation limit:** the remote host served the production assets, but browser-side JavaScript, layout, paint, heap, and input timing were measured on the Windows machine. These measurements cannot certify responsiveness on the target i7-7700.

## Build And Bundle

| Command | Result |
| --- | --- |
| `npm install` | Pass; 75 packages added, 0 vulnerabilities; generated the server checkout lockfile |
| `npm run validate` | Pass; 8/8 tests, typecheck, production build |
| `npm test` | Pass; 8/8 tests |
| `npm run typecheck` | Pass |
| `npm run build` | Pass; Vite 8.3.1 |

| Emitted asset | Raw bytes | Gzip bytes |
| --- | ---: | ---: |
| `dist/index.html` | 461 | 305 |
| `dist/assets/index-Cd8E1oQz.css` | 3,293 | 1,402 |
| `dist/assets/index-54yHZlvq.js` | 26,539 | 10,122 |
| `dist/assets/index-54yHZlvq.js.map` | 161,467 | not measured |
| `dist/favicon.svg` | 238 | not measured |

## Selection Matrix

Each clean matrix point used 120 same-row and 120 cross-row selections. The `sync` p95 is the local state/DOM propagation measurement; `frame` p95 includes scheduling to the next animation frame. `DOM` is the benchmark API's total element count. Heap is a point-in-time Chromium `performance.memory` sample and is sensitive to garbage collection.

### 1440x900

| Retained posts | DOM nodes | Heap MiB | Same sync p95 ms | Same frame p95 ms | Cross sync p95 ms | Cross frame p95 ms | Long Tasks |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 320 | 1,072 | 6.5 | 0.4 | 17.2 | 0.4 | 17.1 | 0 |
| 2,000 | 6,532 | 9.8 | 0.4 | 17.1 | 0.4 | 17.1 | 0 |
| 5,000 | 16,282 | 14.0 | 0.4 | 17.1 | 0.4 | 17.2 | 0 |
| 10,000 | 32,532 | 24.8 | 0.5 | 17.2 | 0.5 | 17.2 | 0 |

### 390x844

| Retained posts | DOM nodes | Heap MiB | Same sync p95 ms | Same frame p95 ms | Cross sync p95 ms | Cross frame p95 ms | Long Tasks |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 320 | 1,312 | 57.9 | 0.4 | 17.1 | 0.4 | 17.2 | 0 |
| 2,000 | 8,032 | 37.2 | 0.4 | 17.2 | 0.4 | 17.1 | 0 |
| 5,000 | 20,032 | 36.5 | 0.4 | 17.1 | 0.5 | 17.2 | 0 |
| 10,000 | 40,032 | 69.8 | 0.4 | 17.1 | 0.5 | 27.8 | 1 |

Within each matrix page, `rowUnmounts` did not increase as retained posts grew (desktop stayed at 848; mobile stayed at 5,688). These are page-lifetime counters, not per-point deltas; prior route/viewport activity contributed to their starting values. Long-scroll runs likewise had no further row unmounts after their initial page setup.

At 10,000 posts the retained board had about 32.5k DOM nodes at desktop width and 40.0k at the two-column mobile width. The mobile 10k cross-row sample had a 9.4 ms sync maximum, 37.3 ms frame maximum, and one recorded Long Task. Do not interpret the viewport emulation as mobile-device performance.

## Scroll And Navigation

Wheel-driven scrolling from `/` reached all 10,000 posts at both viewports and passed invariants. At the 10,000-post point, desktop had 32,521 DOM nodes and 3 Long Tasks during traversal; mobile had 40,021 DOM nodes and 2 Long Tasks. The milestone samples and heap bytes are in the JSON results.

Direct loads of `/post/10000`, `/post/5000`, and `/post/1` selected exactly one matching thumbnail and expanded post, with bounded windows and passing invariants. Same-row selection, cross-row relocation, repeated Back/Forward, Arrow keys, J/K, and Escape all passed.

**Observed issue: prepending newer posts moves the viewport away from the selected post.** From `/post/5000`, extending the newer edge changed the range start from 4,840 to 4,520. At 390x844, scrollY changed from 15,113 px to 459 px and the selected thumbnail's top moved from about 8 px to 44,822 px. At 1440x900, scrollY changed from 1,282 px to 0 and selected-top from 2,326 px to 10,743 px. Invariants still passed, but the selected post was no longer kept at its prior screen position. This is a scrolling correctness/acceptability issue for the M1 gate; it was not fixed in this profiling task.

A rapid repeated `scrollTo(documentHeight)` probe initially stalled at 960 retained posts. Wheel-driven scrolling reached 10,000; this difference suggests the sentinel extension path is sensitive to scroll/intersection timing and merits a repeat during follow-up.

## Performance Traces

Chrome DevTools Protocol tracing covered 320 same-row and cross-row, then 2,000 / 5,000 / 10,000 cross-row selections at each viewport (120 iterations per case). The desktop capture contained 71,923 events; the mobile capture contained 72,540. Aggregate event counts and durations are in the JSON results. Layout/update-style and paint events were present at both sizes; trace event durations overlap and must not be summed as exclusive CPU time.

Tracing perturbed frame timings and occasionally produced tasks above 50 ms; the untraced matrix is the timing comparison. Raw trace JSON could not be persisted because the integrated browser did not emit a download event. The summarized CDP event data is retained in the JSON results.

## Decisions

- **Solid framework gate:** not decided. Selection sync p95 stayed around 0.4-0.5 ms through 10,000 posts in this Windows Chromium run, but the target CPU was not executing the browser workload.
- **Selection scaling:** promising in this run; measured sync p95 changed by at most about 0.2 ms across the retention matrix.
- **Long scrolling:** not accepted yet. Full feed traversal completed, but prepend anchoring jumped substantially and traversal recorded Long Tasks on the Windows host.
- **Retention / virtualization:** no decision. Retained DOM is 32k-40k nodes at 10,000 posts, but no target-browser CPU profile exists and the scrolling issue needs investigation first.
- **M1 architecture readiness:** not ready for gate review or M2. No application behavior or source was changed.

Wallium was active for the small baseline, stopped only for the controlled run, and restored afterward. Its backend, worker, PostgreSQL, and Redis containers were verified running; the profiling preview container and SSH tunnel were removed/closed. The remote isolated checkout and its generated lockfile remain for reproducibility.