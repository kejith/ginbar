import {
  For,
  Show,
  createEffect,
  createMemo,
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
  groupRows,
  makeFakePosts,
  nextPostIndex,
  pathForPost,
  postIdFromPath,
  requiredVisibleCount,
  rowIndexForPostIndex,
} from "./board-model.js";

interface FakePost {
  id: number;
  kind: "image" | "video";
  width: number;
  height: number;
  score: number;
  tags: string[];
}

type HistoryMode = "push" | "replace" | "none";

declare global {
  interface Window {
    __ginbarM1?: {
      select(id: number): void;
      retain(count: number): void;
      run(pattern: "same-row" | "cross-row", iterations?: number): Promise<BenchmarkSummary>;
      stats(): Readonly<BenchmarkStats>;
    };
  }
}

interface BenchmarkStats {
  selectionSamples: number[];
  longTasks: number;
  lastSelectionMs: number;
}

interface BenchmarkSummary {
  iterations: number;
  p50: number;
  p95: number;
  max: number;
}

const posts = makeFakePosts() as FakePost[];
const postIndexById = new Map(posts.map((post, index) => [post.id, index]));
const fakeMedia =
  "data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='1600' height='1000' viewBox='0 0 1600 1000'%3E%3Crect width='1600' height='1000' fill='%23171a1f'/%3E%3Cpath d='M0 850 430 420l260 260 250-310 660 630H0z' fill='%232b313b'/%3E%3C/svg%3E";

const percentile = (samples: number[], fraction: number) => {
  if (samples.length === 0) return 0;
  const sorted = [...samples].sort((a, b) => a - b);
  return sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * fraction))];
};

const App: Component = () => {
  let boardElement!: HTMLDivElement;
  let sentinelElement!: HTMLDivElement;

  const [columns, setColumns] = createSignal(1);
  const [visibleCount, setVisibleCount] = createSignal(INITIAL_POSTS);
  const [selectedId, setSelectedId] = createSignal<number | null>(null);
  const [stats, setStats] = createSignal<BenchmarkStats>({
    selectionSamples: [],
    longTasks: 0,
    lastSelectionMs: 0,
  });

  const visiblePosts = createMemo(() => posts.slice(0, Math.min(visibleCount(), posts.length)));
  const rows = createMemo(() => groupRows(visiblePosts(), columns()) as FakePost[][]);
  const selectedIndex = createMemo(() => {
    const id = selectedId();
    return id === null ? -1 : (postIndexById.get(id) ?? -1);
  });
  const selectedRowIndex = createMemo(() => rowIndexForPostIndex(selectedIndex(), columns()));
  const selectedPost = createMemo(() => {
    const index = selectedIndex();
    return index < 0 ? null : posts[index];
  });
  const benchmarkEnabled = new URLSearchParams(window.location.search).has("bench");

  const markSelectionPaint = (startedAt: number) =>
    new Promise<number>((resolve) => {
      requestAnimationFrame(() => {
        const elapsed = performance.now() - startedAt;
        setStats((previous) => {
          const selectionSamples = [...previous.selectionSamples, elapsed].slice(-240);
          return { ...previous, selectionSamples, lastSelectionMs: elapsed };
        });
        resolve(elapsed);
      });
    });

  const selectPost = (id: number, mode: HistoryMode = "push", ensureVisible = false) => {
    const index = postIndexById.get(id);
    if (index === undefined) return Promise.resolve(0);

    const startedAt = performance.now();
    setVisibleCount((count) => Math.min(POST_COUNT, requiredVisibleCount(index, count)));
    setSelectedId(id);

    if (mode === "push") history.pushState({ postId: id }, "", pathForPost(id));
    if (mode === "replace") history.replaceState({ postId: id }, "", pathForPost(id));

    if (ensureVisible) {
      requestAnimationFrame(() => {
        document.querySelector<HTMLElement>(`[data-post-id="${id}"]`)?.scrollIntoView({ block: "nearest" });
      });
    }

    return markSelectionPaint(startedAt);
  };

  const closePost = () => {
    setSelectedId(null);
    history.pushState({}, "", "/");
  };

  const syncRoute = (mode: HistoryMode = "none") => {
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
    void selectPost(id, mode, true);
  };

  const navigate = (direction: -1 | 1) => {
    const current = selectedIndex();
    const index = current < 0 ? 0 : nextPostIndex(current, direction, posts.length);
    void selectPost(posts[index].id, "push", true);
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

  onMount(() => {
    const resizeObserver = new ResizeObserver(([entry]) => {
      setColumns(columnsForWidth(entry.contentRect.width));
    });
    resizeObserver.observe(boardElement);

    const intersectionObserver = new IntersectionObserver(
      ([entry]) => {
        if (!entry.isIntersecting) return;
        setVisibleCount((count) => Math.min(posts.length, count + LOAD_CHUNK));
      },
      { rootMargin: "1200px 0px" },
    );
    intersectionObserver.observe(sentinelElement);

    const onPopState = () => syncRoute("none");
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
        void selectPost(id, "push", true);
      },
      retain: (count) => {
        const bounded = Math.max(INITIAL_POSTS, Math.min(POST_COUNT, Math.trunc(count)));
        setVisibleCount(bounded);
      },
      run: async (pattern, iterations = 120) => {
        const count = Math.max(1, Math.min(240, Math.trunc(iterations)));
        const samples: number[] = [];
        for (let iteration = 0; iteration < count; iteration += 1) {
          const index = pattern === "same-row"
            ? iteration % columns()
            : (iteration * columns() * 7) % visiblePosts().length;
          samples.push(await selectPost(posts[index].id, "none"));
        }
        samples.sort((a, b) => a - b);
        return {
          iterations: count,
          p50: percentile(samples, 0.5),
          p95: percentile(samples, 0.95),
          max: samples[samples.length - 1] ?? 0,
        };
      },
      stats: () => stats(),
    };

    syncRoute("replace");

    onCleanup(() => {
      resizeObserver.disconnect();
      intersectionObserver.disconnect();
      longTaskObserver?.disconnect();
      window.removeEventListener("popstate", onPopState);
      window.removeEventListener("keydown", onKeyDown);
      delete window.__ginbarM1;
    });
  });

  createEffect(() => {
    boardElement?.style.setProperty("--columns", String(columns()));
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
          <span>{visiblePosts().length.toLocaleString()} retained</span>
        </div>
      </header>

      <div class="board" ref={boardElement} aria-label="Media board">
        <For each={rows()}>
          {(row, rowIndex) => (
            <BoardRow
              row={row}
              rowIndex={rowIndex()}
              selectedId={selectedId}
              selectedRowIndex={selectedRowIndex}
              selectedPost={selectedPost}
              onSelect={(id) => void selectPost(id)}
              onClose={closePost}
            />
          )}
        </For>
      </div>

      <div ref={sentinelElement} class="load-sentinel" aria-hidden="true" />

      <Show when={benchmarkEnabled}>
        <BenchmarkPanel
          stats={stats}
          visibleCount={() => visiblePosts().length}
          rowCount={() => rows().length}
        />
      </Show>
    </main>
  );
};

interface BoardRowProps {
  row: FakePost[];
  rowIndex: number;
  selectedId: Accessor<number | null>;
  selectedRowIndex: Accessor<number>;
  selectedPost: Accessor<FakePost | null>;
  onSelect(id: number): void;
  onClose(): void;
}

const BoardRow: Component<BoardRowProps> = (props) => {
  const expanded = createMemo(() => props.selectedRowIndex() === props.rowIndex);
  return (
    <section class="board-row" classList={{ "board-row--expanded": expanded() }}>
      <div class="thumbnail-row">
        <For each={props.row}>
          {(post) => (
            <button
              type="button"
              class="thumbnail"
              classList={{ "thumbnail--selected": props.selectedId() === post.id }}
              data-post-id={post.id}
              aria-label={`Open post ${post.id}`}
              aria-pressed={props.selectedId() === post.id}
              onClick={() => props.onSelect(post.id)}
              style={`--thumb-hue: ${(post.id * 29) % 360}`}
            >
              <span class="thumbnail-id">#{post.id}</span>
              <span class="thumbnail-kind">{post.kind}</span>
            </button>
          )}
        </For>
      </div>

      <Show when={expanded() && props.selectedPost()} keyed>
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
        <For each={props.post.tags}>{(tag) => <span>{tag}</span>}</For>
      </div>
      <button type="button" onClick={props.onClose}>Close</button>
    </aside>
  </article>
);

const BenchmarkPanel: Component<{
  stats: Accessor<BenchmarkStats>;
  visibleCount: Accessor<number>;
  rowCount: Accessor<number>;
}> = (props) => {
  const p50 = createMemo(() => percentile(props.stats().selectionSamples, 0.5));
  const p95 = createMemo(() => percentile(props.stats().selectionSamples, 0.95));
  return (
    <output class="benchmark-panel">
      <strong>M1 instrumentation</strong>
      <span>last {props.stats().lastSelectionMs.toFixed(1)} ms</span>
      <span>p50 {p50().toFixed(1)} ms</span>
      <span>p95 {p95().toFixed(1)} ms</span>
      <span>{props.stats().longTasks} long tasks</span>
      <span>{props.visibleCount().toLocaleString()} posts / {props.rowCount()} rows</span>
    </output>
  );
};

export default App;
