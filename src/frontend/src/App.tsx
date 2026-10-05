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

import MediaStatus from "./MediaStatus";
import {
  APIError,
  fetchAround,
  fetchCurrentUser,
  fetchFeed,
  type CurrentUser,
  type PostSummary,
} from "./api";
import {
  FEED_PAGE_SIZE,
  MAX_RETAINED_POSTS,
  aroundRadiusForColumns,
  mediaPath,
  mergePostWindows,
  thumbnailStorageKey,
} from "./board-data.js";
import { columnsForWidth, pathForPost, postIdFromPath } from "./board-model.js";
import { boardURL, effectiveSearchQuery, searchQueryFromSearch } from "./search-route.js";

type HistoryMode = "push" | "replace" | "none";
type WindowDirection = "older" | "newer";
type BenchmarkPattern = "same-row" | "cross-row";
type RouteStatus = "idle" | "loading" | "not-found" | "error";
type AuthState =
  | { status: "loading" }
  | { status: "signed-out" }
  | { status: "signed-in"; user: CurrentUser }
  | { status: "unavailable" };

interface ViewportAnchor {
  element: HTMLElement;
  top: number;
}

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
  retainedPosts: number;
  renderedRows: number;
  thumbnails: number;
  domNodes: number;
  rowMounts: number;
  rowUnmounts: number;
  longTasks: number;
  hasNewer: boolean;
  hasOlder: boolean;
  selectedId: number | null;
  routeStatus: RouteStatus;
  searchQuery: string;
  heapBytes: number | null;
}

interface InvariantResult {
  ok: boolean;
  errors: string[];
}

declare global {
  interface Window {
    __ginbarM4?: {
      select(id: number): void;
      search(query: string): void;
      loadOlder(): Promise<void>;
      loadNewer(): Promise<void>;
      run(pattern: BenchmarkPattern, iterations?: number): Promise<BenchmarkSummary>;
      snapshot(): BenchmarkSnapshot;
      assertInvariants(): InvariantResult;
      resetStats(): void;
      stats(): Readonly<BenchmarkStats>;
      ids(): number[];
    };
  }
}

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

const App: Component = () => {
  let boardElement!: HTMLDivElement;
  let topSentinelElement!: HTMLDivElement;
  let bottomSentinelElement!: HTMLDivElement;
  let rowMounts = 0;
  let rowUnmounts = 0;
  let routeSequence = 0;
  let windowEpoch = 0;
  let routeController: AbortController | undefined;
  let initialController: AbortController | undefined;
  let windowController: AbortController | undefined;
  let windowLoadPromise: Promise<void> | null = null;

  const initialSearchQuery = searchQueryFromSearch(window.location.search);
  const [columns, setColumns] = createSignal(1);
  const [posts, setPosts] = createSignal<PostSummary[]>([]);
  const [selectedId, setSelectedId] = createSignal<number | null>(null);
  const [routePostId, setRoutePostId] = createSignal<number | null>(null);
  const [routeStatus, setRouteStatus] = createSignal<RouteStatus>("idle");
  const [authState, setAuthState] = createSignal<AuthState>({ status: "loading" });
  const [hasOlder, setHasOlder] = createSignal(false);
  const [hasNewer, setHasNewer] = createSignal(false);
  const [initialLoading, setInitialLoading] = createSignal(false);
  const [feedError, setFeedError] = createSignal(false);
  const [searchQuery, setSearchQuery] = createSignal(initialSearchQuery);
  const [searchDraft, setSearchDraft] = createSignal(initialSearchQuery);
  const [searchError, setSearchError] = createSignal<string | null>(null);
  const [stats, setStats] = createSignal<BenchmarkStats>({
    selectionSamples: [],
    longTasks: 0,
    lastSyncMs: 0,
    lastFrameMs: 0,
  });

  const postById = createMemo(() => new Map(posts().map((post) => [post.id, post])));
  const selectedPost = createMemo(() => {
    const id = selectedId();
    return id === null ? null : (postById().get(id) ?? null);
  });
  const selectedIndex = createMemo(() => {
    const id = selectedId();
    if (id === null) return -1;
    return posts().findIndex((post) => post.id === id);
  });
  const selectedRowKey = createMemo(() => {
    const index = selectedIndex();
    if (index < 0) return null;
    const rowStart = Math.floor(index / columns()) * columns();
    return posts()[rowStart]?.id ?? null;
  });
  const isSelectedPost = createSelector(selectedId);
  const isSelectedRow = createSelector(selectedRowKey);

  const rowMap = createMemo(() => {
    const value = new Map<number, PostSummary[]>();
    const list = posts();
    const width = columns();
    for (let start = 0; start < list.length; start += width) {
      const row = list.slice(start, start + width);
      if (row[0]) value.set(row[0].id, row);
    }
    return value;
  });
  const rowKeys = createMemo(() => [...rowMap().keys()]);
  const authLabel = createMemo(() => {
    const state = authState();
    if (state.status === "signed-in") return state.user.username;
    if (state.status === "signed-out") return "signed out";
    if (state.status === "unavailable") return "auth unavailable";
    return "auth…";
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

  const scrollSelectedIntoView = (id: number) => {
    requestAnimationFrame(() => {
      boardElement
        .querySelector<HTMLElement>(`[data-post-id="${id}"]`)
        ?.scrollIntoView({ block: "nearest" });
    });
  };

  const writeHistory = (id: number | null, mode: HistoryMode, query = searchQuery()) => {
    const path = id === null ? "/" : pathForPost(id);
    const url = boardURL(path, query, benchmarkEnabled);
    const currentURL = `${window.location.pathname}${window.location.search}`;
    const state = {
      ...(id === null ? {} : { postId: id }),
      ...(query === "" ? {} : { q: query }),
    };
    if (mode === "push" && currentURL !== url) {
      history.pushState(state, "", url);
    } else if (mode === "replace") {
      history.replaceState(state, "", url);
    }
  };

  const selectRetainedPost = (
    id: number,
    mode: HistoryMode = "push",
    ensureVisible = false,
    measure = false,
    recordSample = measure,
  ): Promise<SelectionTiming> | null => {
    if (!postById().has(id)) return null;

    const startedAt = performance.now();
    setRoutePostId(id);
    setRouteStatus("idle");
    setSelectedId(id);
    writeHistory(id, mode);
    const syncMs = performance.now() - startedAt;

    if (ensureVisible) scrollSelectedIntoView(id);
    return measure ? recordNextFrame(startedAt, syncMs, recordSample) : null;
  };

  const closePost = () => {
    setSelectedId(null);
    setRoutePostId(null);
    setRouteStatus("idle");
    writeHistory(null, "push");
  };

  const replaceServerWindow = (nextPosts: PostSummary[]) => {
    windowEpoch += 1;
    setPosts(nextPosts);
  };

  const resetForSearchChange = () => {
    initialController?.abort();
    initialController = undefined;
    setInitialLoading(false);
    routeController?.abort();
    routeController = undefined;
    routeSequence += 1;
    windowController?.abort();
    replaceServerWindow([]);
    setSelectedId(null);
    setHasNewer(false);
    setHasOlder(false);
    setRouteStatus("idle");
    setFeedError(false);
  };

  const loadInitialFeed = async (query = searchQuery()) => {
    if (initialController || posts().length > 0) return;
    const controller = new AbortController();
    initialController = controller;
    setInitialLoading(true);
    setFeedError(false);
    try {
      const page = await fetchFeed(0, FEED_PAGE_SIZE, query, controller.signal);
      if (controller.signal.aborted || routePostId() !== null || query !== searchQuery()) return;
      replaceServerWindow(page.posts);
      setHasNewer(false);
      setHasOlder(Boolean(page.nextBefore));
      setSearchError(null);
    } catch (error) {
      if (controller.signal.aborted || query !== searchQuery()) return;
      if (error instanceof APIError && error.code === "invalid_search") setSearchError(error.message);
      else setFeedError(true);
    } finally {
      if (initialController === controller) {
        initialController = undefined;
        setInitialLoading(false);
      }
    }
  };

  const loadRoutePost = async (id: number, query = searchQuery()) => {
    initialController?.abort();
    initialController = undefined;
    setInitialLoading(false);
    routeController?.abort();
    const controller = new AbortController();
    routeController = controller;
    const sequence = ++routeSequence;
    const radius = aroundRadiusForColumns(columns());
    setSelectedId(null);
    setRouteStatus("loading");
    setFeedError(false);

    try {
      const result = await fetchAround(id, radius, query, controller.signal);
      if (
        controller.signal.aborted
        || sequence !== routeSequence
        || routePostId() !== id
        || query !== searchQuery()
      ) return;
      if (result === null) {
        setRouteStatus("not-found");
        return;
      }
      if (result.selectedId !== id) throw new Error("around response selected id mismatch");

      replaceServerWindow(result.posts);
      const index = result.posts.findIndex((post) => post.id === id);
      if (index < 0) throw new Error("around response omitted selected post");
      setHasNewer(index >= radius);
      setHasOlder(result.posts.length - index - 1 >= radius);
      setSelectedId(id);
      setRouteStatus("idle");
      setSearchError(null);
      scrollSelectedIntoView(id);
    } catch (error) {
      if (controller.signal.aborted || query !== searchQuery()) return;
      if (error instanceof APIError && error.status === 404) setRouteStatus("not-found");
      else if (error instanceof APIError && error.code === "invalid_search") {
        setSearchError(error.message);
        setRouteStatus("error");
      } else setRouteStatus("error");
    } finally {
      if (routeController === controller) routeController = undefined;
    }
  };

  const syncRoute = () => {
    const nextQuery = searchQueryFromSearch(window.location.search);
    const queryChanged = nextQuery !== searchQuery();
    setSearchDraft(nextQuery);
    if (queryChanged) {
      setSearchQuery(nextQuery);
      setSearchError(null);
      resetForSearchChange();
    }

    const id = postIdFromPath(window.location.pathname);
    setRoutePostId(id);
    routeController?.abort();

    if (id === null) {
      setSelectedId(null);
      setRouteStatus("idle");
      if (posts().length === 0) void loadInitialFeed(nextQuery);
      return;
    }
    if (postById().has(id)) {
      selectRetainedPost(id, "none", true);
      return;
    }
    void loadRoutePost(id, nextQuery);
  };

  const applySearch = (value = searchDraft()) => {
    const nextQuery = effectiveSearchQuery(value);
    setSearchDraft(nextQuery);
    const id = postIdFromPath(window.location.pathname);
    writeHistory(id, "push", nextQuery);
    syncRoute();
  };

  const captureViewportAnchor = (): ViewportAnchor | null => {
    const topbarBottom = document.querySelector<HTMLElement>(".topbar")?.getBoundingClientRect().bottom ?? 0;
    const x = Math.min(Math.max(window.innerWidth / 2, 0), Math.max(0, window.innerWidth - 1));
    const y = Math.min(Math.max(topbarBottom + 1, 0), Math.max(0, window.innerHeight - 1));
    const element = document.elementFromPoint(x, y)?.closest<HTMLElement>("[data-row-key]") ?? null;
    return element ? { element, top: element.getBoundingClientRect().top } : null;
  };

  const restoreViewportAnchor = (anchor: ViewportAnchor | null) => {
    if (!anchor) return;
    requestAnimationFrame(() => {
      if (!anchor.element.isConnected) return;
      const shiftedTop = anchor.element.getBoundingClientRect().top;
      const correction = shiftedTop - anchor.top;
      if (Math.abs(correction) > 0.5) window.scrollBy(0, correction);
    });
  };

  const loadWindow = (direction: WindowDirection): Promise<void> => {
    if (windowLoadPromise) return windowLoadPromise;
    const list = posts();
    if (list.length === 0) return Promise.resolve();
    if (direction === "older" && !hasOlder()) return Promise.resolve();
    if (direction === "newer" && !hasNewer()) return Promise.resolve();

    const epoch = windowEpoch;
    const query = searchQuery();
    const controller = new AbortController();
    windowController = controller;
    windowLoadPromise = (async () => {
      try {
        if (direction === "older") {
          const boundary = list[list.length - 1].id;
          const page = await fetchFeed(boundary, FEED_PAGE_SIZE, query, controller.signal);
          if (controller.signal.aborted || epoch !== windowEpoch || query !== searchQuery()) return;
          if (page.posts.length === 0) {
            setHasOlder(false);
            return;
          }
          const merged = mergePostWindows(
            posts(),
            page.posts,
            "older",
            selectedId(),
            columns(),
            MAX_RETAINED_POSTS,
          );
          setPosts(merged.posts);
          setHasOlder(Boolean(page.nextBefore));
          if (merged.trimmedNewer > 0) setHasNewer(true);
          return;
        }

        const boundary = list[0].id;
        const radius = aroundRadiusForColumns(columns());
        const result = await fetchAround(boundary, radius, query, controller.signal);
        if (controller.signal.aborted || epoch !== windowEpoch || query !== searchQuery()) return;
        if (result === null) {
          setHasNewer(false);
          return;
        }
        const incoming = result.posts.filter((post) => post.id > boundary);
        if (incoming.length === 0) {
          setHasNewer(false);
          return;
        }
        const anchor = captureViewportAnchor();
        const merged = mergePostWindows(
          posts(),
          incoming,
          "newer",
          selectedId(),
          columns(),
          MAX_RETAINED_POSTS,
        );
        setPosts(merged.posts);
        setHasNewer(incoming.length >= radius);
        if (merged.trimmedOlder > 0) setHasOlder(true);
        restoreViewportAnchor(anchor);
      } catch (error) {
        if (controller.signal.aborted || epoch !== windowEpoch || query !== searchQuery()) return;
        if (error instanceof APIError && error.code === "invalid_search") setSearchError(error.message);
        else setFeedError(true);
      }
    })().finally(() => {
      if (windowController === controller) windowController = undefined;
      windowLoadPromise = null;
    });
    return windowLoadPromise;
  };

  const navigate = async (direction: -1 | 1) => {
    let list = posts();
    if (list.length === 0) return;
    const currentId = selectedId();
    if (currentId === null) {
      selectRetainedPost(list[0].id, "push", true, benchmarkEnabled, benchmarkEnabled);
      return;
    }

    let index = list.findIndex((post) => post.id === currentId);
    if (index < 0) return;
    let target = index + direction;
    if (target >= 0 && target < list.length) {
      selectRetainedPost(list[target].id, "push", true, benchmarkEnabled, benchmarkEnabled);
      return;
    }

    if (direction === 1 && hasOlder()) await loadWindow("older");
    else if (direction === -1 && hasNewer()) await loadWindow("newer");
    else return;

    list = posts();
    index = list.findIndex((post) => post.id === currentId);
    target = index + direction;
    if (index >= 0 && target >= 0 && target < list.length) {
      selectRetainedPost(list[target].id, "push", true, benchmarkEnabled, benchmarkEnabled);
    }
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
      void navigate(1);
      return;
    }
    if (event.key === "ArrowLeft" || event.key === "ArrowUp" || event.key.toLowerCase() === "k") {
      event.preventDefault();
      void navigate(-1);
    }
  };

  const snapshot = (): BenchmarkSnapshot => {
    const memory = (performance as Performance & { memory?: { usedJSHeapSize: number } }).memory;
    return {
      columns: columns(),
      retainedPosts: posts().length,
      renderedRows: rowKeys().length,
      thumbnails: boardElement.querySelectorAll(".thumbnail").length,
      domNodes: document.getElementsByTagName("*").length,
      rowMounts,
      rowUnmounts,
      longTasks: stats().longTasks,
      hasNewer: hasNewer(),
      hasOlder: hasOlder(),
      selectedId: selectedId(),
      routeStatus: routeStatus(),
      searchQuery: searchQuery(),
      heapBytes: memory?.usedJSHeapSize ?? null,
    };
  };

  const assertInvariants = (): InvariantResult => {
    const errors: string[] = [];
    const list = posts();
    if (list.length > MAX_RETAINED_POSTS) errors.push(`retained ${list.length} posts above bound`);
    for (let index = 1; index < list.length; index += 1) {
      if (list[index - 1].id <= list[index].id) {
        errors.push("retained posts are not strictly ID-descending");
        break;
      }
    }
    if (new Set(list.map((post) => post.id)).size !== list.length) errors.push("retained posts contain duplicates");

    const id = selectedId();
    const expanded = boardElement.querySelectorAll<HTMLElement>("[data-expanded-post]");
    const pressed = boardElement.querySelectorAll<HTMLElement>('.thumbnail[aria-pressed="true"]');
    if (id === null) {
      if (expanded.length !== 0) errors.push(`expected no expanded post, found ${expanded.length}`);
      if (pressed.length !== 0) errors.push(`expected no selected thumbnail, found ${pressed.length}`);
    } else {
      if (!postById().has(id)) errors.push("selected post is not retained");
      if (expanded.length !== 1) errors.push(`expected one expanded post, found ${expanded.length}`);
      if (expanded[0]?.dataset.expandedPost !== String(id)) errors.push("expanded post does not match selected id");
      if (pressed.length !== 1) errors.push(`expected one selected thumbnail, found ${pressed.length}`);
      if (pressed[0]?.dataset.postId !== String(id)) errors.push("selected thumbnail does not match selected id");
      if (expanded[0]?.closest<HTMLElement>("[data-row-key]")?.dataset.rowKey !== String(selectedRowKey())) {
        errors.push("expanded post is not inside the selected thumbnail row");
      }
    }
    return { ok: errors.length === 0, errors };
  };

  const resetStats = () => {
    setStats({ selectionSamples: [], longTasks: 0, lastSyncMs: 0, lastFrameMs: 0 });
  };

  const runBenchmark = async (pattern: BenchmarkPattern, iterations = 120): Promise<BenchmarkSummary> => {
    const rows = [...rowMap().values()];
    let pair: [number, number] | null = null;
    if (pattern === "same-row") {
      const row = rows.find((candidate) => candidate.length > 1);
      if (row) pair = [row[0].id, row[1].id];
    } else if (rows.length > 1) {
      pair = [rows[0][0].id, rows[1][0].id];
    }
    if (!pair) return summarize([]);

    const count = Math.max(1, Math.min(240, Math.trunc(iterations)));
    const samples: SelectionTiming[] = [];
    for (let iteration = 0; iteration < count; iteration += 1) {
      const measured = selectRetainedPost(pair[iteration & 1], "none", false, true, false);
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

  onMount(() => {
    const applyBoardWidth = (width: number) => {
      const nextColumns = columnsForWidth(width);
      const gapPx = 2;
      const rowSize = Math.max(1, (width - Math.max(0, nextColumns - 1) * gapPx) / nextColumns + gapPx);
      boardElement.style.setProperty("--columns", String(nextColumns));
      boardElement.style.setProperty("--row-size", `${rowSize}px`);
      setColumns(nextColumns);
    };

    applyBoardWidth(boardElement.getBoundingClientRect().width || window.innerWidth);
    const resizeObserver = new ResizeObserver(([entry]) => applyBoardWidth(entry.contentRect.width));
    resizeObserver.observe(boardElement);

    const intersectionObserver = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (!entry.isIntersecting) continue;
          if (entry.target === topSentinelElement && hasNewer()) void loadWindow("newer");
          if (entry.target === bottomSentinelElement && hasOlder()) void loadWindow("older");
        }
      },
      { rootMargin: "1000px 0px" },
    );
    intersectionObserver.observe(topSentinelElement);
    intersectionObserver.observe(bottomSentinelElement);

    const authController = new AbortController();
    void fetchCurrentUser(authController.signal)
      .then((user) => setAuthState(user ? { status: "signed-in", user } : { status: "signed-out" }))
      .catch(() => {
        if (!authController.signal.aborted) setAuthState({ status: "unavailable" });
      });

    const onPopState = () => syncRoute();
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
        // Long Tasks is not available in every browser; selection timings remain usable.
      }
    }

    window.__ginbarM4 = {
      select: (id) => {
        selectRetainedPost(id, "push", true, benchmarkEnabled, benchmarkEnabled);
      },
      search: (query) => applySearch(query),
      loadOlder: () => loadWindow("older"),
      loadNewer: () => loadWindow("newer"),
      run: runBenchmark,
      snapshot,
      assertInvariants,
      resetStats,
      stats: () => stats(),
      ids: () => posts().map((post) => post.id),
    };

    syncRoute();

    onCleanup(() => {
      resizeObserver.disconnect();
      intersectionObserver.disconnect();
      authController.abort();
      routeController?.abort();
      initialController?.abort();
      windowController?.abort();
      longTaskObserver?.disconnect();
      window.removeEventListener("popstate", onPopState);
      window.removeEventListener("keydown", onKeyDown);
      delete window.__ginbarM4;
    });
  });

  return (
    <main class="app-shell">
      <header class="topbar">
        <div>
          <strong>Ginbar v2</strong>
          <span>{posts().length.toLocaleString()} retained</span>
        </div>
        <div class="topbar-meta">
          <span>{authLabel()}</span>
          <span>{columns()} cols</span>
        </div>
      </header>

      <form
        class="search-bar"
        role="search"
        onSubmit={(event) => {
          event.preventDefault();
          applySearch();
        }}
      >
        <label for="board-search">Search</label>
        <input
          id="board-search"
          type="search"
          value={searchDraft()}
          placeholder="tag -excluded score:>=100"
          autocomplete="off"
          spellcheck={false}
          onInput={(event) => setSearchDraft(event.currentTarget.value)}
        />
        <button type="submit">Apply</button>
        <Show when={searchQuery() !== ""}>
          <button type="button" onClick={() => applySearch("")}>Clear</button>
        </Show>
      </form>

      <Show when={searchError()}>
        {(message) => (
          <p class="board-message board-message--error" role="alert">Invalid search: {message()}</p>
        )}
      </Show>
      <Show when={feedError()}>
        <p class="board-message board-message--error" role="status">Feed update failed</p>
      </Show>
      <Show when={routeStatus() !== "idle" && routePostId() !== null}>
        <RouteShell postId={routePostId()!} status={routeStatus()} />
      </Show>
      <Show when={initialLoading() && posts().length === 0 && routePostId() === null}>
        <p class="board-message" role="status">Loading feed…</p>
      </Show>

      <div class="board" ref={boardElement} aria-label="Media board">
        <div ref={topSentinelElement} class="load-sentinel" aria-hidden="true" />
        <For each={rowKeys()}>
          {(rowKey: number) => (
            <BoardRow
              rowKey={rowKey}
              posts={() => rowMap().get(rowKey) ?? []}
              isSelectedPost={isSelectedPost}
              isSelectedRow={isSelectedRow}
              selectedPost={selectedPost}
              onSelect={(id) => selectRetainedPost(id, "push", false, benchmarkEnabled, benchmarkEnabled)}
              onClose={closePost}
              onMountRow={() => { rowMounts += 1; }}
              onUnmountRow={() => { rowUnmounts += 1; }}
            />
          )}
        </For>
        <div ref={bottomSentinelElement} class="load-sentinel" aria-hidden="true" />
      </div>

      <Show when={benchmarkEnabled}>
        <BenchmarkPanel stats={stats} retainedCount={() => posts().length} rowCount={() => rowKeys().length} />
      </Show>
    </main>
  );
};

interface BoardRowProps {
  rowKey: number;
  posts: Accessor<PostSummary[]>;
  isSelectedPost(id: number): boolean;
  isSelectedRow(rowKey: number): boolean;
  selectedPost: Accessor<PostSummary | null>;
  onSelect(id: number): void;
  onClose(): void;
  onMountRow(): void;
  onUnmountRow(): void;
}

const BoardRow: Component<BoardRowProps> = (props) => {
  onMount(props.onMountRow);
  onCleanup(props.onUnmountRow);

  return (
    <section
      class="board-row"
      classList={{ "board-row--expanded": props.isSelectedRow(props.rowKey) }}
      data-row-key={props.rowKey}
    >
      <div class="thumbnail-row">
        <For each={props.posts()}>
          {(post: PostSummary) => (
            <Thumbnail post={post} selected={props.isSelectedPost(post.id)} onSelect={props.onSelect} />
          )}
        </For>
      </div>

      <Show when={props.isSelectedRow(props.rowKey) && props.selectedPost()} keyed>
        {(post: PostSummary) => <ExpandedPost post={post} onClose={props.onClose} />}
      </Show>
    </section>
  );
};

const Thumbnail: Component<{ post: PostSummary; selected: boolean; onSelect(id: number): void }> = (props) => {
  const thumbnailURL = createMemo(() => safeThumbnailPath(props.post.media.storageKey));
  return (
    <button
      type="button"
      class="thumbnail"
      classList={{ "thumbnail--selected": props.selected }}
      data-post-id={props.post.id}
      aria-label={`Open post ${props.post.id}`}
      aria-pressed={props.selected}
      onClick={() => props.onSelect(props.post.id)}
    >
      <Show when={thumbnailURL()} fallback={<span class="thumbnail-placeholder" aria-hidden="true" />}>
        {(url) => <img src={url()} width="256" height="256" alt="" loading="lazy" decoding="async" />}
      </Show>
      <span class="thumbnail-id">#{props.post.id}</span>
      <span class="thumbnail-score">{props.post.score}</span>
    </button>
  );
};

const ExpandedPost: Component<{ post: PostSummary; onClose(): void }> = (props) => {
  const mediaURL = createMemo(() => safeMediaPath(props.post.media.storageKey));
  const thumbnailURL = createMemo(() => safeThumbnailPath(props.post.media.storageKey));
  return (
    <article class="expanded-post" data-expanded-post={props.post.id}>
      <div class="expanded-media">
        <Show when={mediaURL()} fallback={<p class="media-unavailable">Media unavailable</p>}>
          {(url) => (
            <Show
              when={props.post.media.kind === 1}
              fallback={
                <img
                  src={url()}
                  width={props.post.media.width}
                  height={props.post.media.height}
                  alt={`Post ${props.post.id}`}
                  decoding="async"
                />
              }
            >
              <video
                src={url()}
                width={props.post.media.width}
                height={props.post.media.height}
                controls
                preload="metadata"
                poster={thumbnailURL() ?? undefined}
                aria-label={`Video post ${props.post.id}`}
              />
            </Show>
          )}
        </Show>
      </div>
      <aside class="expanded-meta">
        <div>
          <strong>#{props.post.id}</strong>
          <span>{props.post.score} points</span>
          <span>by user {props.post.authorId}</span>
          <span>{props.post.media.width}×{props.post.media.height}</span>
        </div>
        <MediaStatus postId={props.post.id} />
        <button type="button" onClick={props.onClose}>Close</button>
      </aside>
    </article>
  );
};

const RouteShell: Component<{ postId: number; status: RouteStatus }> = (props) => (
  <article class="route-shell" data-route-shell={props.postId} role="status">
    <strong>Post #{props.postId}</strong>
    <span>
      {props.status === "loading"
        ? "Loading surrounding feed…"
        : props.status === "not-found"
          ? "Post not found"
          : "Post could not be loaded"}
    </span>
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
      <strong>M4 instrumentation</strong>
      <span>sync {props.stats().lastSyncMs.toFixed(2)} ms</span>
      <span>frame {props.stats().lastFrameMs.toFixed(1)} ms</span>
      <span>sync p95 {syncP95().toFixed(2)} ms</span>
      <span>frame p95 {frameP95().toFixed(1)} ms</span>
      <span>{props.stats().longTasks} long tasks</span>
      <span>{props.retainedCount().toLocaleString()} posts / {props.rowCount()} rows</span>
    </output>
  );
};

function safeMediaPath(storageKey: string): string | null {
  try {
    return mediaPath(storageKey);
  } catch {
    return null;
  }
}

function safeThumbnailPath(storageKey: string): string | null {
  try {
    return mediaPath(thumbnailStorageKey(storageKey));
  } catch {
    return null;
  }
}

export default App;
