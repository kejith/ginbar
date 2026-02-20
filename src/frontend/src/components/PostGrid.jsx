import { useRef, useEffect, useCallback, useMemo } from "react";
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
}) {
  const { posts, page, hasMore, listLoading, fetchPosts } = usePostStore();
  const cols = useColumns();
  const parentRef = useRef(null);
  const initialScrollDone = useRef(false);

  // Build virtual row descriptors
  const rows = useMemo(
    () => buildVirtualRows(posts, cols, expandedId, listLoading),
    [posts, cols, expandedId, listLoading],
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

  // ── Infinite scroll ───────────────────────────────────────────────────────
  const virtualItems = virtualizer.getVirtualItems();

  useEffect(() => {
    if (!virtualItems.length) return;
    const last = virtualItems[virtualItems.length - 1];
    // When the last visible item is within 3 rows of the end, fetch more
    if (last.index >= rows.length - 3 && hasMore && !listLoading) {
      fetchPosts({ page: page + 1, tag, reset: false });
    }
  }, [virtualItems, rows.length, hasMore, listLoading, page, tag, fetchPosts]);

  // ── Scroll so the clicked card row is at the top (panel appears right below nav)
  useEffect(() => {
    if (expandedId == null) return;
    // Target the card row, not the expanded panel — this guarantees the panel
    // starts exactly at the top edge of the scroll viewport.
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

  // ── Initial expanded post from URL (on mount) ─────────────────────────────
  useEffect(() => {
    if (initialExpanded && !initialScrollDone.current) {
      initialScrollDone.current = true;
      setExpandedId(initialExpanded);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialExpanded]);

  // ── Preload full-size image as soon as a post is expanded ──────────────────
  useEffect(() => {
    if (expandedId == null) return;
    const post = posts.find((p) => p.id === expandedId);
    if (!post || !post.filename) return;
    const isVideo =
      post.content_type?.startsWith("video/") ||
      post.filename.match(/\.(mp4|webm|mov)$/i);
    if (!isVideo) {
      const img = new window.Image();
      img.src = `/images/${post.filename}`;
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
    } else if (hasMore && !listLoading) {
      // Fetch next page; once posts update the effect will find the new post
      await fetchPosts({ page: page + 1, tag, reset: false });
      // After fetch, pick the first newly arrived post (index = currentPostIndex + 1)
      // The posts array will update and we rely on the next render to resolve.
      // We peek at the store directly after await.
      const updated = usePostStore.getState().posts;
      if (updated.length > currentPostIndex + 1) {
        const nextId = updated[currentPostIndex + 1].id;
        setExpandedId(nextId);
        onPostOpen?.(nextId);
      }
    }
  }, [
    currentPostIndex,
    posts,
    hasMore,
    listLoading,
    fetchPosts,
    page,
    tag,
    setExpandedId,
    onPostOpen,
  ]);

  const canGoNewer = currentPostIndex > 0;
  const canGoOlder = currentPostIndex < posts.length - 1 || hasMore;

  // ── Render ────────────────────────────────────────────────────────────────
  const totalHeight = virtualizer.getTotalSize();

  return (
    <div
      ref={parentRef}
      className="flex-1 min-h-0 overflow-y-auto"
      style={{ contain: "strict" }}
    >
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
                  className="grid gap-1 px-2 py-0.5"
                  style={{
                    gridTemplateColumns: `repeat(${cols}, minmax(0, 1fr))`,
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
                  {/* Pad incomplete last row with skeletons */}
                  {row.items.length < cols &&
                    !hasMore &&
                    Array.from({
                      length: cols - row.items.length,
                    }).map((_, i) => <PostCardSkeleton key={`sk-${i}`} />)}
                </div>
              )}

              {row.type === "expanded" && (
                <InlinePost
                  postId={row.postId}
                  onClose={handleClose}
                  onNewer={handleNewer}
                  onOlder={handleOlder}
                  canGoNewer={canGoNewer}
                  canGoOlder={canGoOlder}
                />
              )}

              {row.type === "loading" && (
                <div
                  className="grid gap-1 px-2 py-0.5"
                  style={{
                    gridTemplateColumns: `repeat(${cols}, minmax(0, 1fr))`,
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

      {/* Empty state (only when not loading and no posts loaded at all) */}
      {!listLoading && posts.length === 0 && (
        <p className="absolute inset-0 flex items-center justify-center text-sm text-(--color-muted)">
          nothing here yet
        </p>
      )}
    </div>
  );
}
