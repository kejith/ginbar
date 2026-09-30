import {
  For,
  Show,
  createMemo,
  createSelector,
  createSignal,
  onCleanup,
  onMount,
} from "solid-js";
import type { Accessor, Component } from "solid-js";

import {
  INITIAL_POSTS,
  LOAD_CHUNK,
  POST_COUNT,
  columnsForWidth,
  extendRange,
  extendRangeToIndex,
  makeFakePosts,
  nextPostIndex,
  pathForPost,
  postIdFromPath,
  rangeAroundIndex,
  rowIndexForPostIndex,
  rowRangeForIndexRange,
} from "./board-model.js";

interface FakePost {
  id: number;
  kind: "image" | "video";
  width: number;
  height: number;
  score: number;
  tags: string[];
}

interface IndexRange {
  start: number;
  end: number;
}

type HistoryMode = "push" | "replace" | "none";
type LoadMode = "extend" | "center";
type BenchmarkPattern = "same-row" | "cross-row";

interface SelectionTiming {
  syncMs: number;
  frameMs: number;
}

interface Percentiles {
  p50: number;
  p95: number;
  max: number;
}

interface BenchmarkSummary {
  iterations: number;
  sync: Percentiles;
  frame: Percentiles;
}

interface BenchmarkStats {
  selectionSamples: SelectionTiming[];
  longTasks: number;
  lastSyncMs: number;
  lastFrameMs: number;
}

interface BenchmarkSnapshot {
  columns: number;
  logicalRange: IndexRange;
  renderedRows: number;
  thumbnails: number;
  domNodes: number;
  rowMounts: number;
  rowUnmounts: number;
  longTasks: number;
  heapBytes: number | null;
}

interface MatrixResult {
  requestedPosts: number;
  snapshot: BenchmarkSnapshot;
  sameRow: BenchmarkSummary;
  crossRow: BenchmarkSummary;
  invariants: InvariantResult;
}

interface InvariantResult {
  ok: boolean;
  errors: string[];
}

declare global {
  interface Window {
    __ginbarM1?: {
      select(id: number): void;
      retain(count: number): Promise<BenchmarkSnapshot>;
      run(pattern: BenchmarkPattern, iterations?: number): Promise<BenchmarkSummary>;
      runMatrix(iterations?: number): Promise<MatrixResult[]>;
      snapshot(): BenchmarkSnapshot;
      assertInvariants(): InvariantResult;
      resetStats(): void;
      stats(): Readonly<BenchmarkStats>;
    };
  }
}

const posts = makeFakePosts() as FakePost[];
const postIndexById = new Map(posts.map((post, index) => [post.id, index]));
const fakeMedia =
  "data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='1600' height='1000' viewBox='0 0 1600 1000'%3E%3Crect width='1600' height='1000' fill='%23171a1f'/%3E%3Cpath d='M0 850 430 420l260 260 250-310 660 630H0z' fill='%232b313b'/%3E%3C/svg%3E";

const percentile = (samples: number[], fraction: number) => {
  if (samples.length === 0) return 0;
  const sorted = [...samples].sort((a, b) => a - b);
  return sorted[Math.min(sorted.length - 1, Math.floor((sorted.length - 1) * fraction))];
};

const summarize = (samples: SelectionTiming[]): BenchmarkSummary => {
  const sync = samples.map((sample) => sample.syncMs);
  const frame = samples.map((sample) => sample.frameMs);
  return {
    iterations: samples.length,
    sync: { p50: percentile(sync, 0.5), p95: percentile(sync, 0.95), max: Math.max(0, ...sync) },
    frame: { p50: percentile(frame, 0.5), p95: percentile(frame, 0.95), max: Math.max(0, ...frame) },
  };
};

const settleFrames = (count = 2) =>
  new Promise<void>((resolve) => {
    let remaining = Math.max(1, count);
    const step = () => {
      remaining -= 1;
      if (remaining === 0) resolve();
      else requestAnimationFrame(step);
    };
    requestAnimationFrame(step);
  });

const App: Component = () => {
  let boardElement!: HTMLDivElement;
  let topSentinelElement!: HTMLDivElement;
  let bottomSentinelElement!: HTMLDivElement;
  let rowMounts = 0;
  let rowUnmounts = 0;

  const [columns, setColumns] = createSignal(1);
  const [loadedRange, setLoadedRange] = createSignal<IndexRange>({ start: 0, end: INITIAL_POSTS });
  const [selectedId, setSelectedId] = createSignal<number | null>(null);
  const [stats, setStats] = createSignal<BenchmarkStats>({
    selectionSamples: [],
    longTasks: 0,
    lastSyncMs: 0,
    lastFrameMs: 0,
  });

  const selectedIndex = createMemo(() => {
    const id = selectedId();
    return id === null ? -1 : (postIndexById.get(id) ?? -1);
  });
  const selectedRowIndex = createMemo(() => rowIndexForPostIndex(selectedIndex(), columns()));
  const selectedPost = createMemo(() => {
    const index = selectedIndex();
    return index < 0 ? null : posts[index];
  });
  const isSelectedPost = createSelector(selectedId);
  const isSelectedRow = createSelector(selectedRowIndex);

  const loadedRowRange = createMemo(() => rowRangeForIndexRange(loadedRange(), columns()));
  const rowIndices = createMemo(() => {
    const range = loadedRowRange();
    return Array.from({ length: range.end - range.start }, (_, offset) => range.start + offset);
  });
  const renderedPostCount = createMemo(() => {
    const range = loadedRowRange();
    const start = range.start * columns();
    const end = Math.min(posts.length, range.end * columns());
    return Math.max(0, end - start);
  });

  const benchmarkEnabled = new URLSearchParams(window.location.search).has("bench");

  const recordNextFrame = (startedAt: number, syncMs: number, recordSample: boolean) =>
    new Promise<SelectionTiming>((resolve) => {
      requestAnimationFrame(() => {
        const timing = { syncMs, frameMs: performance.now() - startedAt };
        if (recordSample) {
          setStats((previous) => ({
            ...previous,
            selectionSamples: [...previous.selectionSamples, timing].slice(-240),
            lastSyncMs: timing.syncMs,
            lastFrameMs: timing.frameMs,
          }));
        }
        resolve(timing);
      });
    });

  const ensureIndexLoaded = (index: number, mode: LoadMode) => {
    if (mode === "center") {
      setLoadedRange(rangeAroundIndex(index, posts.length));
      return;
    }
    const range = loadedRange();
    if (index >= range.start && index < range.end) return;
    const farOutside = index < range.start - LOAD_CHUNK || index >= range.end + LOAD_CHUNK;
    setLoadedRange(
      farOutside
        ? rangeAroundIndex(index, posts.length)
        : extendRangeToIndex(range, index, posts.length),
    );
  };

  const selectPost = (
    id: number,
    mode: HistoryMode = "push",
    ensureVisible = false,
    loadMode: LoadMode = "extend",
    measure = false,
    recordSample = measure,
  ): Promise<SelectionTiming> | null => {
    const index = postIndexById.get(id);
    if (index === undefined) return null;

    const startedAt = performance.now();
    ensureIndexLoaded(index, loadMode);
    setSelectedId(id);

    const path = pathForPost(id);
    if (mode === "push" && window.location.pathname !== path) history.pushState({ postId: id }, "", path);
    if (mode === "replace") history.replaceState({ postId: id }, "", path);

    const syncMs = performance.now() - startedAt;

    if (ensureVisible) {
      requestAnimationFrame(() => {
        boardElement.querySelector<HTMLElement>(`[data-post-id="${id}"]`)?.scrollIntoView({ block: "nearest" });
      });
    }

    return measure ? recordNextFrame(startedAt, syncMs, recordSample) : null;
  };

  const closePost = () => {
    setSelectedId(null);
    history.pushState({}, "", "/");
  };

  const syncRoute = (mode: HistoryMode = "none", loadMode: LoadMode = "extend") => {
    const id = postIdFromPath(window.location.pathname);
    if (id === null) {
      setSelectedId(null);
      return;
    }
    if (!postIndexById.has(id)) {
      setSelectedId(null);
      if (mode !== "none") history.replaceState({}, "", "/");
      return;
    }
    selectPost(id, mode, true, loadMode);
  };

  const navigate = (direction: -1 | 1) => {
    const current = selectedIndex();
    const startIndex = current < 0 ? loadedRange().start : current;
    const index = current < 0 ? startIndex : nextPostIndex(startIndex, direction, posts.length);
    selectPost(posts[index].id, "push", true, "extend", benchmarkEnabled, benchmarkEnabled);
  };

  const onKeyDown = (event: KeyboardEvent) => {
    const target = event.target as HTMLElement | null;
    if (target?.closest("input, textarea, select, [contenteditable='true']")) return;

    if (event.key === "Escape" && selectedId() !== null) {
      event.preventDefault();
      closePost();
      return;
    }

    if (event.key === "ArrowRight" || event.key === "ArrowDown" || event.key.toLowerCase() === "j") {
      event.preventDefault();
      navigate(1);
      return;
    }

    if (event.key === "ArrowLeft" || event.key === "ArrowUp" || event.key.toLowerCase() === "k") {
      event.preventDefault();
      navigate(-1);
    }
  };

  const snapshot = (): BenchmarkSnapshot => {
    const memory = (performance as Performance & { memory?: { usedJSHeapSize: number } }).memory;
    return {
      columns: columns(),
      logicalRange: { ...loadedRange() },
      renderedRows: rowIndices().length,
      thumbnails: boardElement.querySelectorAll(".thumbnail").length,
      domNodes: document.getElementsByTagName("*").length,
      rowMounts,
      rowUnmounts,
      longTasks: stats().longTasks,
      heapBytes: memory?.usedJSHeapSize ?? null,
    };
  };

  const assertInvariants = (): InvariantResult => {
    const errors: string[] = [];
    const id = selectedId();
    const expanded = boardElement.querySelectorAll<HTMLElement>("[data-expanded-post]");
    const pressed = boardElement.querySelectorAll<HTMLElement>('.thumbnail[aria-pressed="true"]');

    if (id === null) {
      if (expanded.length !== 0) errors.push(`expected no expanded post, found ${expanded.length}`);
      if (pressed.length !== 0) errors.push(`expected no selected thumbnail, found ${pressed.length}`);
    } else {
      if (expanded.length !== 1) errors.push(`expected one expanded post, found ${expanded.length}`);
      if (expanded[0]?.dataset.expandedPost !== String(id)) errors.push("expanded post does not match selected id");
      if (pressed.length !== 1) errors.push(`expected one selected thumbnail, found ${pressed.length}`);
      if (pressed[0]?.dataset.postId !== String(id)) errors.push("selected thumbnail does not match selected id");
      const expectedRow = String(selectedRowIndex());
      if (expanded[0]?.closest<HTMLElement>("[data-row-index]")?.dataset.rowIndex !== expectedRow) {
        errors.push("expanded post is not inside the selected thumbnail row");
      }
    }

    return { ok: errors.length === 0, errors };
  };

  const resetStats = () => {
    setStats({ selectionSamples: [], longTasks: 0, lastSyncMs: 0, lastFrameMs: 0 });
  };

  const retainForBenchmark = async (count: number) => {
    const bounded = Math.max(INITIAL_POSTS, Math.min(POST_COUNT, Math.trunc(count)));
    setLoadedRange({ start: 0, end: bounded });
    setSelectedId(null);
    window.scrollTo(0, 0);
    await settleFrames(2);
    return snapshot();
  };

  const runBenchmark = async (pattern: BenchmarkPattern, iterations = 120): Promise<BenchmarkSummary> => {
    const count = Math.max(1, Math.min(240, Math.trunc(iterations)));
    const rowRange = loadedRowRange();
    const baseIndex = Math.min(posts.length - 1, rowRange.start * columns());
    const sameIndex = Math.min(posts.length - 1, baseIndex + Math.min(1, columns() - 1));
    const crossIndex = Math.min(posts.length - 1, baseIndex + columns());
    const pair = pattern === "same-row" ? [baseIndex, sameIndex] : [baseIndex, crossIndex];
    const samples: SelectionTiming[] = [];

    for (let iteration = 0; iteration < count; iteration += 1) {
      const index = pair[iteration & 1];
      const measured = selectPost(posts[index].id, "none", false, "extend", true, false);
      if (measured) samples.push(await measured);
    }
    const summary = summarize(samples);
    const last = samples[samples.length - 1];
    if (last) {
      setStats((previous) => ({
        ...previous,
        selectionSamples: samples.slice(-240),
        lastSyncMs: last.syncMs,
        lastFrameMs: last.frameMs,
      }));
    }
    return summary;
  };

  const runMatrix = async (iterations = 120): Promise<MatrixResult[]> => {
    resetStats();
    const results: MatrixResult[] = [];
    for (const requestedPosts of [320, 2_000, 5_000, 10_000]) {
      await retainForBenchmark(requestedPosts);
      const sameRow = await runBenchmark("same-row", iterations);
      const crossRow = await runBenchmark("cross-row", iterations);
      results.push({
        requestedPosts,
        snapshot: snapshot(),
        sameRow,
        crossRow,
        invariants: assertInvariants(),
      });
    }
    return results;
  };

  onMount(() => {
    const resizeObserver = new ResizeObserver(([entry]) => {
      const width = entry.contentRect.width;
      const nextColumns = columnsForWidth(width);
      const gapPx = 2;
      const rowSize = Math.max(1, (width - Math.max(0, nextColumns - 1) * gapPx) / nextColumns + gapPx);
      boardElement.style.setProperty("--columns", String(nextColumns));
      boardElement.style.setProperty("--row-size", `${rowSize}px`);
      setColumns(nextColumns);
    });
    resizeObserver.observe(boardElement);

    const intersectionObserver = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (!entry.isIntersecting) continue;
          if (entry.target === topSentinelElement) {
            setLoadedRange((range) => extendRange(range, posts.length, -1, LOAD_CHUNK));
          } else if (entry.target === bottomSentinelElement) {
            setLoadedRange((range) => extendRange(range, posts.length, 1, LOAD_CHUNK));
          }
        }
      },
      { rootMargin: "1200px 0px" },
    );
    intersectionObserver.observe(topSentinelElement);
    intersectionObserver.observe(bottomSentinelElement);

    const onPopState = () => syncRoute("none", "extend");
    window.addEventListener("popstate", onPopState);
    window.addEventListener("keydown", onKeyDown);

    let longTaskObserver: PerformanceObserver | undefined;
    if ("PerformanceObserver" in window) {
      try {
        longTaskObserver = new PerformanceObserver((list) => {
          const count = list.getEntries().length;
          if (count > 0) setStats((previous) => ({ ...previous, longTasks: previous.longTasks + count }));
        });
        longTaskObserver.observe({ type: "longtask", buffered: true });
      } catch {
        // Safari currently lacks the Long Tasks API; selection timings still work.
      }
    }

    window.__ginbarM1 = {
      select: (id) => {
        selectPost(id, "push", true, "extend", benchmarkEnabled, benchmarkEnabled);
      },
      retain: retainForBenchmark,
      run: runBenchmark,
      runMatrix,
      snapshot,
      assertInvariants,
      resetStats,
      stats: () => stats(),
    };

    syncRoute("replace", "center");

    onCleanup(() => {
      resizeObserver.disconnect();
      intersectionObserver.disconnect();
      longTaskObserver?.disconnect();
      window.removeEventListener("popstate", onPopState);
      window.removeEventListener("keydown", onKeyDown);
      delete window.__ginbarM1;
    });
  });

  return (
    <main class="app-shell">
      <header class="topbar">
        <div>
          <strong>Ginbar v2 / M1</strong>
          <span>{posts.length.toLocaleString()} synthetic posts</span>
        </div>
        <div class="topbar-meta">
          <span>{columns()} cols</span>
          <span>{renderedPostCount().toLocaleString()} retained</span>
        </div>
      </header>

      <div class="board" ref={boardElement} aria-label="Media board">
        <div ref={topSentinelElement} class="load-sentinel" aria-hidden="true" />
        <For each={rowIndices()}>
          {(rowIndex: number) => (
            <BoardRow
              rowIndex={rowIndex}
              columns={columns}
              isSelectedPost={isSelectedPost}
              isSelectedRow={isSelectedRow}
              selectedPost={selectedPost}
              onSelect={(id) => selectPost(id, "push", false, "extend", benchmarkEnabled, benchmarkEnabled)}
              onClose={closePost}
              onMountRow={() => { rowMounts += 1; }}
              onUnmountRow={() => { rowUnmounts += 1; }}
            />
          )}
        </For>
        <div ref={bottomSentinelElement} class="load-sentinel" aria-hidden="true" />
      </div>

      <Show when={benchmarkEnabled}>
        <BenchmarkPanel
          stats={stats}
          retainedCount={renderedPostCount}
          rowCount={() => rowIndices().length}
        />
      </Show>
    </main>
  );
};

interface BoardRowProps {
  rowIndex: number;
  columns: Accessor<number>;
  isSelectedPost(id: number): boolean;
  isSelectedRow(rowIndex: number): boolean;
  selectedPost: Accessor<FakePost | null>;
  onSelect(id: number): void;
  onClose(): void;
  onMountRow(): void;
  onUnmountRow(): void;
}

const BoardRow: Component<BoardRowProps> = (props) => {
  const row = createMemo(() => {
    const columns = props.columns();
    const start = props.rowIndex * columns;
    return posts.slice(start, Math.min(posts.length, start + columns));
  });

  onMount(props.onMountRow);
  onCleanup(props.onUnmountRow);

  return (
    <section
      class="board-row"
      classList={{ "board-row--expanded": props.isSelectedRow(props.rowIndex) }}
      data-row-index={props.rowIndex}
    >
      <div class="thumbnail-row">
        <For each={row()}>
          {(post: FakePost) => (
            <button
              type="button"
              class="thumbnail"
              classList={{ "thumbnail--selected": props.isSelectedPost(post.id) }}
              data-post-id={post.id}
              aria-label={`Open post ${post.id}`}
              aria-pressed={props.isSelectedPost(post.id)}
              onClick={() => props.onSelect(post.id)}
              style={`--thumb-hue: ${(post.id * 29) % 360}`}
            >
              <span class="thumbnail-id">#{post.id}</span>
              <span class="thumbnail-kind">{post.kind}</span>
            </button>
          )}
        </For>
      </div>

      <Show when={props.isSelectedRow(props.rowIndex) && props.selectedPost()} keyed>
        {(post: FakePost) => <ExpandedPost post={post} onClose={props.onClose} />}
      </Show>
    </section>
  );
};

const ExpandedPost: Component<{ post: FakePost; onClose(): void }> = (props) => (
  <article class="expanded-post" data-expanded-post={props.post.id}>
    <div class="expanded-media">
      <Show
        when={props.post.kind === "video"}
        fallback={
          <img
            src={fakeMedia}
            width={props.post.width}
            height={props.post.height}
            alt={`Synthetic post ${props.post.id}`}
            decoding="async"
          />
        }
      >
        <video controls preload="metadata" poster={fakeMedia} aria-label={`Synthetic video post ${props.post.id}`} />
      </Show>
    </div>
    <aside class="expanded-meta">
      <div>
        <strong>#{props.post.id}</strong>
        <span>{props.post.score} points</span>
      </div>
      <div class="tag-list">
        <For each={props.post.tags}>{(tag: string) => <span>{tag}</span>}</For>
      </div>
      <button type="button" onClick={props.onClose}>Close</button>
    </aside>
  </article>
);

const BenchmarkPanel: Component<{
  stats: Accessor<BenchmarkStats>;
  retainedCount: Accessor<number>;
  rowCount: Accessor<number>;
}> = (props) => {
  const syncP95 = createMemo(() => percentile(props.stats().selectionSamples.map((sample) => sample.syncMs), 0.95));
  const frameP95 = createMemo(() => percentile(props.stats().selectionSamples.map((sample) => sample.frameMs), 0.95));
  return (
    <output class="benchmark-panel">
      <strong>M1 instrumentation</strong>
      <span>sync {props.stats().lastSyncMs.toFixed(2)} ms</span>
      <span>frame {props.stats().lastFrameMs.toFixed(1)} ms</span>
      <span>sync p95 {syncP95().toFixed(2)} ms</span>
      <span>frame p95 {frameP95().toFixed(1)} ms</span>
      <span>{props.stats().longTasks} long tasks</span>
      <span>{props.retainedCount().toLocaleString()} posts / {props.rowCount()} rows</span>
    </output>
  );
};

export default App;
