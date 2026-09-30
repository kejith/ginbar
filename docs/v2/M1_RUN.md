# M1 hardware runbook

Use this on the target machine after pulling `astra/m1-board-prototype`. Do not merge to `v2` until the real Solid/Vite build and browser profile are reviewed.

## 1. Install and validate

From `src/frontend`:

```bash
npm install
npm run validate
```

Expected outcomes:

- `package-lock.json` is generated from the current Solid/Vite dependency graph.
- all board-model tests pass;
- `tsc --noEmit` passes against the real Solid/Vite declarations;
- the production Vite build succeeds;
- inspect the generated bundle sizes before accepting the framework gate.

If install/build fails, preserve the exact error in `docs/v2/STATE.md` before changing dependencies.

## 2. Start the prototype

```bash
npm run dev -- --host 0.0.0.0
```

Use a clean Chromium profile for the main measurements. Disable unrelated extensions. Keep DevTools closed for the first automated timing matrix, then repeat representative cases with the Performance panel open.

The benchmark API is exposed as `window.__ginbarM1` on every page. Adding `?bench=1` only shows the on-screen timing panel and instruments normal click/keyboard selections; omit it for the cleanest automated run.

## 3. Automated retention matrix

On `/`, run in the console:

```js
const results = await window.__ginbarM1.runMatrix(120);
console.table(results.map(({ requestedPosts, snapshot, sameRow, crossRow, invariants }) => ({
  posts: requestedPosts,
  thumbnails: snapshot.thumbnails,
  rows: snapshot.renderedRows,
  domNodes: snapshot.domNodes,
  heapMiB: snapshot.heapBytes == null ? null : +(snapshot.heapBytes / 1048576).toFixed(1),
  rowMounts: snapshot.rowMounts,
  rowUnmounts: snapshot.rowUnmounts,
  sameSyncP95: +sameRow.sync.p95.toFixed(3),
  crossSyncP95: +crossRow.sync.p95.toFixed(3),
  sameFrameP95: +sameRow.frame.p95.toFixed(2),
  crossFrameP95: +crossRow.frame.p95.toFixed(2),
  longTasks: snapshot.longTasks,
  invariants: invariants.ok,
})));
```

Save the full `results` object as JSON with the machine/browser details. The matrix grows retained DOM monotonically through 320, 2,000, 5,000, and 10,000 posts. `rowUnmounts` should remain zero during that growth; a large increase would indicate the stable-row-key assumption is wrong in the real Solid runtime.

`sync.p95` measures local state/DOM propagation before the next frame is requested. `frame.p95` includes scheduling to the next animation frame and therefore tends toward the display refresh interval; do not interpret it as pure scripting time. DevTools Performance is the source of truth for scripting/layout/paint attribution.

## 4. Performance recordings

For both 1440x900 and 390x844 viewport sizes, record representative traces for:

```js
await window.__ginbarM1.retain(320);
await window.__ginbarM1.run("same-row", 120);
await window.__ginbarM1.run("cross-row", 120);

await window.__ginbarM1.retain(2000);
await window.__ginbarM1.run("cross-row", 120);

await window.__ginbarM1.retain(5000);
await window.__ginbarM1.run("cross-row", 120);

await window.__ginbarM1.retain(10000);
await window.__ginbarM1.run("cross-row", 120);
```

Look specifically for:

- selection work growing with retained DOM count;
- unexpected row remounts;
- long tasks;
- layout/paint spikes from moving the expanded row;
- memory growth that does not correspond to retained DOM;
- costly work caused by `content-visibility` or intrinsic-size placeholders;
- whole-board style/layout invalidation.

Do not add virtualization unless these traces identify retained DOM as the actual bottleneck.

## 5. Direct-link and history checks

Direct-load these routes in a fresh tab:

- `/post/10000`
- `/post/5000`
- `/post/1`

After each load run:

```js
window.__ginbarM1.snapshot();
window.__ginbarM1.assertInvariants();
```

A direct link should reconstruct a bounded window around the selected post rather than rendering every newer post. The selected post must have exactly one selected thumbnail and exactly one expanded row in its deterministic absolute row.

Then verify manually:

1. select multiple posts in the same row;
2. select a post in another row;
3. use Back and Forward repeatedly;
4. navigate with Left/Right, Up/Down, J/K;
5. press Escape to close;
6. from `/post/5000`, scroll toward both newer and older posts so both range sentinels extend the feed.

Run `window.__ginbarM1.assertInvariants()` after each sequence if anything looks suspicious.

## 6. Long-scroll retention check

At 390x844 and 1440x900:

- start from `/` and scroll until all 10,000 posts have been retained;
- record DOM node count and heap at 320 / 2,000 / 5,000 / 10,000 posts;
- repeat from `/post/5000` while extending in both directions;
- watch for scrollbar jumps when rows are prepended;
- verify the selected expanded row does not disappear or relocate incorrectly while new rows are added.

## 7. Gate decision

Keep SolidJS only if the production build and real traces show no framework-specific architectural red flag for the board interaction. If retained DOM becomes the bottleneck, first test a bounded-retention strategy that preserves absolute row mapping and browser-history behavior; evaluate virtualization only if that evidence is insufficient.

Before ending the hardware session, update `docs/v2/STATE.md` with:

- Node/npm/Chromium versions and viewport/device assumptions;
- production bundle sizes;
- matrix results;
- representative Performance trace findings;
- DOM/heap growth observations;
- direct-link/history/keyboard correctness results;
- Solid framework decision;
- retention/virtualization decision;
- the single next task.
