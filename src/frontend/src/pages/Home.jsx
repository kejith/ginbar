import { useEffect, useState } from "react";
import { useParams, useSearchParams } from "react-router-dom";
import PostGrid from "../components/PostGrid.jsx";
import usePostStore from "../stores/postStore.js";

export default function Home() {
  // postIdParam is only set when mounting directly via /post/:postId
  // (shared link / bookmark). Within the running app we use replaceState so
  // the component never unmounts and the grid keeps its scroll position.
  const { postId: postIdParam } = useParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const query = searchParams.get("q") || "";
  const tag = searchParams.get("tag") || "";
  const commentIdParam = searchParams.get("comment");
  const highlightCommentId = commentIdParam ? Number(commentIdParam) : null;
  const initialExpanded = postIdParam ? Number(postIdParam) : null;

  const [expandedId, setExpandedId] = useState(initialExpanded);

  const {
    listError,
    fetchPosts,
    fetchAroundPost,
    search,
    activeFilters,
    resetKey,
  } = usePostStore();

  // When the logo is clicked, resetKey increments → close any open post and
  // clear the URL back to "/" (the feed refetch is triggered by resetHome itself).
  useEffect(() => {
    if (resetKey === 0) return; // skip the initial mount
    setExpandedId(null);
    window.history.replaceState(null, "", "/");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [resetKey]);

  // Initial data load
  useEffect(() => {
    if (query) {
      search(query);
    } else if (initialExpanded && !tag) {
      // Load a window of posts centered on the target post — O(1) round trips,
      // no page-chasing. Cursor-mode bi-directional scroll takes over after.
      fetchAroundPost(initialExpanded);
    } else {
      fetchPosts({ page: 1, tag: tag || undefined, reset: true });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, tag, activeFilters]);

  function handlePostOpen(id) {
    setExpandedId(id);
    // Silently update the address bar without a React Router navigation so the
    // grid component stays mounted and keeps its virtualised scroll state.
    if (!query && !tag) {
      window.history.replaceState(null, "", `/post/${id}`);
    } else {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          next.set("post", id);
          return next;
        },
        { replace: true },
      );
    }
  }

  function handlePostClose() {
    setExpandedId(null);
    if (!query && !tag) {
      window.history.replaceState(null, "", "/");
    } else {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          next.delete("post");
          return next;
        },
        { replace: true },
      );
    }
  }

  return (
    <div
      className="flex flex-col"
      style={{ height: "calc(100vh - var(--nav-height))" }}
    >
      {(query || tag) && (
        <p className="px-3 pt-2 pb-1 text-sm text-(--color-muted)">
          {query ? `results for "${query}"` : `tag: ${tag}`}
        </p>
      )}

      {listError && (
        <p className="px-3 pt-2 pb-1 text-sm text-(--color-danger)">
          {listError}
        </p>
      )}

      <PostGrid
        tag={tag || undefined}
        initialExpanded={initialExpanded}
        expandedId={expandedId}
        setExpandedId={(id) => {
          if (id == null) handlePostClose();
          else handlePostOpen(id);
        }}
        onPostOpen={handlePostOpen}
        onPostClose={handlePostClose}
        highlightCommentId={highlightCommentId}
      />
    </div>
  );
}
