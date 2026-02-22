import {
  useRef,
  useEffect,
  useLayoutEffect,
  useCallback,
  useMemo,
} from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import PostCard from "./PostCard.jsx";
import PostCardSkeleton from "./PostCardSkeleton.jsx";
import InlinePost from "./InlinePost.jsx";
import useColumns from "../utils/useColumns.js";
import { buildVirtualRows } from "../utils/gridRows.js";
import usePostStore from "../stores/postStore.js";

// Height estimates per row type (px). The expanded panel is measured dynamically.
const CARD_HEIGHT = 260; // thumbnail (aspect-square ≈ card width) + footer
const LOADING_HEIGHT = 80;

/**
 * PostGrid
 *
 * Props:
 *   tag             string|undefined  — active tag filter
 *   initialExpanded number|null       — post id to open on mount (from ?post= param)
 *   onPostOpen      (id) => void      — called when a post is expanded (URL sync)
 *   onPostClose     () => void        — called when expanded panel is closed
 *   expandedId      number|null       — controlled expanded post id
 *   setExpandedId   (id|null) => void — setter for controlled expanded id
 */
export default function PostGrid({
  tag,
  initialExpanded,
  onPostOpen,
  onPostClose,
  expandedId,
  setExpandedId,
  highlightCommentId,
}) {
  const {
    posts,
    page,
    hasMore,
    listLoading,
    fetchPosts,
    // cursor-mode state (active when a direct /post/:id URL is opened)
    cursorMode,
    hasOlderPosts,
    hasNewerPosts,
    olderLoading,
    newerLoading,
    loadOlderPosts,
    loadNewerPosts,
  } = usePostStore();

  const cols = useColumns();
  const parentRef = useRef(null);
  const initialScrollDone = useRef(false);
  // True until the initial scroll-to-target has been performed once.
  const needsInitialScroll = useRef(!!initialExpanded);
  // Becomes true after the final retry timeout fires — prevents near-top
  // load-newer from triggering while initial-scroll retries are still pending.
  const scrollSettled = useRef(!initialExpanded);
  // Holds pending setTimeout IDs for the initial-scroll retries so they can
  // be cancelled if a prepend happens before they fire.
  const scrollRetryIds = useRef([]);
  // Saved before a newer-posts prepend so we can compensate the scroll position.
  const prependRef = useRef(null);

  // ── Virtual row descriptors ───────────────────────────────────────────────
  // In cursor mode the bottom loader is driven by olderLoading; in page mode
  // by listLoading.
  const isLoadingBottom = cursorMode ? olderLoading : listLoading;
  const rows = useMemo(
    () => buildVirtualRows(posts, cols, expandedId, isLoadingBottom),
    [posts, cols, expandedId, isLoadingBottom],
  );

  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => parentRef.current,
    estimateSize: (i) => {
      const row = rows[i];
      if (!row) return CARD_HEIGHT;
      if (row.type === "expanded") return 700;
      if (row.type === "loading") return LOADING_HEIGHT;
      return CARD_HEIGHT;
    },
    overscan: 4,
    measureElement:
      typeof window !== "undefined"
        ? (el) => el.getBoundingClientRect().height
        : undefined,
  });

  // ── Prepend scroll-anchor compensation ───────────────────────────────────
  // When newer posts are prepended the total virtual height grows. We save
  // scrollTop + totalHeight just before the prepend, then in a layout-effect
  // (fires before paint, after React's DOM mutations) restore
  // scrollTop += delta so the existing content stays in place on screen.
  useLayoutEffect(() => {
    if (!prependRef.current || !parentRef.current) return;
    const delta = virtualizer.getTotalSize() - prependRef.current.totalHeight;
    if (delta > 0) {
      parentRef.current.scrollTop = prependRef.current.scrollTop + delta;
    }
    prependRef.current = null;
  }, [rows]); // eslint-disable-line react-hooks/exhaustive-deps — runs on every rows change

  // ── Bidirectional infinite scroll ────────────────────────────────────────
  const virtualItems = virtualizer.getVirtualItems();

  useEffect(() => {
    if (!virtualItems.length) return;
    const first = virtualItems[0];
    const last = virtualItems[virtualItems.length - 1];

    // Near-top → load newer posts (cursor mode only).
    // Guard: don't trigger while the initial-scroll retries are still pending;
    // doing so would start a prepend that fights the in-flight retries.
    if (
      cursorMode &&
      scrollSettled.current &&
      first.index <= 2 &&
      hasNewerPosts &&
      !newerLoading
    ) {
      // Cancel any stale initial-scroll retries before anchoring the scroll.
      scrollRetryIds.current.forEach(clearTimeout);
      scrollRetryIds.current = [];
      if (parentRef.current) {
        prependRef.current = {
          scrollTop: parentRef.current.scrollTop,
          totalHeight: virtualizer.getTotalSize(),
        };
      }
      loadNewerPosts();
    }

    // Near-bottom → load older posts
    if (last.index >= rows.length - 3) {
      if (cursorMode) {
        if (hasOlderPosts && !olderLoading) loadOlderPosts();
      } else {
        if (hasMore && !listLoading)
          fetchPosts({ page: page + 1, tag, reset: false });
      }
    }
  }, [
    virtualItems,
    rows.length,
    cursorMode,
    hasNewerPosts,
    newerLoading,
    hasOlderPosts,
    olderLoading,
    hasMore,
    listLoading,
    page,
    tag,
    fetchPosts,
    loadOlderPosts,
    loadNewerPosts,
    virtualizer,
  ]);

  // ── Scroll so the clicked card row is at the top (panel appears below nav)
  useEffect(() => {
    if (expandedId == null) return;
    const cardRowIdx = rows.findIndex(
      (r) => r.type === "posts" && r.items.some((p) => p.id === expandedId),
    );
    if (cardRowIdx !== -1) {
      virtualizer.scrollToIndex(cardRowIdx, {
        align: "start",
        behavior: "auto",
      });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [expandedId]); // intentionally omit rows/virtualizer — only trigger on id change

  // ── Initial scroll: fires when rows update and the target row first appears.
  //    scrollToIndex uses estimated row heights for unmeasured rows, so the
  //    first call may land slightly off. We retry with setTimeout delays so
  //    ResizeObserver has time to measure newly-rendered rows and the
  //    virtualizer can recalculate the accurate offset.
  //    Each retry first checks whether we're already at the correct row;
  //    if so it's a no-op so subsequent retries don't shift a correct position.
  useEffect(() => {
    if (!needsInitialScroll.current) return;
    const cardRowIdx = rows.findIndex(
      (r) =>
        r.type === "posts" && r.items.some((p) => p.id === initialExpanded),
    );
    if (cardRowIdx === -1) return;

    needsInitialScroll.current = false;

    const scroll = () => {
      // If the target row is already the first visible item, we're done.
      const firstVisible = virtualizer.getVirtualItems()[0];
      if (firstVisible && firstVisible.index === cardRowIdx) return;
      virtualizer.scrollToIndex(cardRowIdx, {
        align: "start",
        behavior: "auto",
      });
    };

    scroll();
    scrollSettled.current = false;
    const ids = [50, 150, 350, 600].map((d, i, arr) =>
      setTimeout(() => {
        scroll();
        // Mark settled after the final retry fires.
        if (i === arr.length - 1) scrollSettled.current = true;
      }, d),
    );
    scrollRetryIds.current = ids;
    return () => {
      ids.forEach(clearTimeout);
      scrollRetryIds.current = [];
      // rows changed while retries were still pending → the virtualizer has
      // re-measured and the layout is now stable.  Mark settled so the
      // near-top trigger can fire, otherwise scrollSettled stays false forever.
      scrollSettled.current = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rows]);

  // ── Open expanded panel once the target post is in the list ──────────────
  useEffect(() => {
    if (!initialExpanded || initialScrollDone.current) return;
    const found = posts.some((p) => p.id === initialExpanded);
    if (found) {
      initialScrollDone.current = true;
      setExpandedId(initialExpanded);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialExpanded, posts]);

  // ── Preload full-size images for the expanded post + 2 neighbours ────────
  useEffect(() => {
    if (expandedId == null) return;
    const idx = posts.findIndex((p) => p.id === expandedId);
    if (idx === -1) return;
    const start = Math.max(0, idx - 2);
    const end = Math.min(posts.length - 1, idx + 2);
    for (let i = start; i <= end; i++) {
      const p = posts[i];
      if (!p?.filename) continue;
      const isVideo =
        p.content_type?.startsWith("video/") ||
        p.filename.match(/\.(mp4|webm|mov)$/i);
      if (!isVideo) {
        const img = new window.Image();
        img.src = `/images/${p.filename}`;
      }
    }
  }, [expandedId, posts]);

  // ── Handlers ──────────────────────────────────────────────────────────────
  const handleExpand = useCallback(
    (id) => {
      if (expandedId === id) {
        setExpandedId(null);
        onPostClose?.();
      } else {
        setExpandedId(id);
        onPostOpen?.(id);
      }
    },
    [expandedId, setExpandedId, onPostClose, onPostOpen],
  );

  const handleClose = useCallback(() => {
    setExpandedId(null);
    onPostClose?.();
  }, [setExpandedId, onPostClose]);

  const currentPostIndex = useMemo(
    () =>
      expandedId != null ? posts.findIndex((p) => p.id === expandedId) : -1,
    [expandedId, posts],
  );

  const handleNewer = useCallback(() => {
    if (currentPostIndex <= 0) return;
    const nextId = posts[currentPostIndex - 1].id;
    setExpandedId(nextId);
    onPostOpen?.(nextId);
  }, [currentPostIndex, posts, setExpandedId, onPostOpen]);

  const handleOlder = useCallback(async () => {
    if (currentPostIndex === -1) return;
    if (currentPostIndex < posts.length - 1) {
      const nextId = posts[currentPostIndex + 1].id;
      setExpandedId(nextId);
      onPostOpen?.(nextId);
    } else if (cursorMode) {
      if (hasOlderPosts && !olderLoading) {
        await loadOlderPosts();
        const updated = usePostStore.getState().posts;
        if (updated.length > currentPostIndex + 1) {
          const nextId = updated[currentPostIndex + 1].id;
          setExpandedId(nextId);
          onPostOpen?.(nextId);
        }
      }
    } else {
      if (hasMore && !listLoading) {
        await fetchPosts({ page: page + 1, tag, reset: false });
        const updated = usePostStore.getState().posts;
        if (updated.length > currentPostIndex + 1) {
          const nextId = updated[currentPostIndex + 1].id;
          setExpandedId(nextId);
          onPostOpen?.(nextId);
        }
      }
    }
  }, [
    currentPostIndex,
    posts,
    cursorMode,
    hasOlderPosts,
    olderLoading,
    loadOlderPosts,
    hasMore,
    listLoading,
    fetchPosts,
    page,
    tag,
    setExpandedId,
    onPostOpen,
  ]);

  const canGoNewer = currentPostIndex > 0;
  const canGoOlder =
    currentPostIndex < posts.length - 1 ||
    (cursorMode ? hasOlderPosts : hasMore);

  // Pad skeletons for incomplete last row only in page mode.
  const showPadSkeletons = !cursorMode && !hasMore;

  // ── Render ────────────────────────────────────────────────────────────────
  const totalHeight = virtualizer.getTotalSize();

  return (
    <div
      ref={parentRef}
      className="flex-1 min-h-0 overflow-y-auto"
      style={{ contain: "strict" }}
    >
      {/* Loading-newer skeleton row — sticky at top, outside the virtual DOM
          so it doesn't shift virtual indices. Negative bottom margin means
          it overlaps the first row instead of pushing content down. */}
      {newerLoading && (
        <div
          className="sticky top-0 z-10 pointer-events-none"
          style={{
            marginBottom: `-${LOADING_HEIGHT}px`,
            height: LOADING_HEIGHT,
          }}
        >
          <div
            className="grid px-2 py-0.5"
            style={{
              gridTemplateColumns: `repeat(${cols}, minmax(0, 1fr))`,
              gap: "var(--grid-gap)",
            }}
          >
            {Array.from({ length: cols }).map((_, i) => (
              <PostCardSkeleton key={i} />
            ))}
          </div>
        </div>
      )}

      <div style={{ height: totalHeight, position: "relative" }}>
        {virtualizer.getVirtualItems().map((vItem) => {
          const row = rows[vItem.index];
          if (!row) return null;

          return (
            <div
              key={vItem.key}
              data-index={vItem.index}
              ref={virtualizer.measureElement}
              style={{
                position: "absolute",
                top: 0,
                left: 0,
                width: "100%",
                transform: `translateY(${vItem.start}px)`,
              }}
            >
              {row.type === "posts" && (
                <div
                  className="grid px-2 py-0.5"
                  style={{
                    gridTemplateColumns: `repeat(${cols}, minmax(0, 1fr))`,
                    gap: "var(--grid-gap)",
                  }}
                >
                  {row.items.map((post) => (
                    <PostCard
                      key={post.id}
                      post={post}
                      onExpand={handleExpand}
                      isExpanded={post.id === expandedId}
                    />
                  ))}
                  {/* Pad incomplete last row with ghost cards */}
                  {row.items.length < cols &&
                    showPadSkeletons &&
                    Array.from({
                      length: cols - row.items.length,
                    }).map((_, i) => <PostCardSkeleton key={`sk-${i}`} />)}
                </div>
              )}

              {row.type === "expanded" && (
                <InlinePost
                  postId={row.postId}
                  listPost={posts.find((p) => p.id === row.postId) ?? null}
                  onClose={handleClose}
                  onNewer={handleNewer}
                  onOlder={handleOlder}
                  canGoNewer={canGoNewer}
                  canGoOlder={canGoOlder}
                  highlightCommentId={highlightCommentId}
                />
              )}

              {row.type === "loading" && (
                <div
                  className="grid px-2 py-0.5"
                  style={{
                    gridTemplateColumns: `repeat(${cols}, minmax(0, 1fr))`,
                    gap: "var(--grid-gap)",
                  }}
                >
                  {Array.from({ length: cols }).map((_, i) => (
                    <PostCardSkeleton key={i} />
                  ))}
                </div>
              )}
            </div>
          );
        })}
      </div>

      {/* Empty state */}
      {!listLoading && posts.length === 0 && (
        <p className="absolute inset-0 flex items-center justify-center text-sm text-(--color-muted)">
          nothing here yet
        </p>
      )}
    </div>
  );
}
