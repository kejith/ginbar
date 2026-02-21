import { useEffect, useState } from "react";
import { useParams, Link } from "react-router-dom";
import PostGrid from "../components/PostGrid.jsx";
import usePostStore from "../stores/postStore.js";

// ── Routes handled ────────────────────────────────────────────────────────────
//   /user/:name/posts
//   /user/:name/posts/:segment      segment = postId (digits) OR tags (string)
//   /user/:name/posts/:tags/:postId

export default function UserGrid() {
  const { name, segment, tags: tagsParam, postId: postIdParam } = useParams();

  // Resolve tags filter and initial expanded post from path params.
  const isSegmentPostId = segment != null && /^\d+$/.test(segment);
  const tagsFilter = tagsParam ?? (!isSegmentPostId ? (segment ?? "") : "");
  const initialExpandedId = postIdParam
    ? Number(postIdParam)
    : isSegmentPostId
      ? Number(segment)
      : null;

  const [expandedId, setExpandedId] = useState(initialExpandedId);

  const { listError, fetchPostsByUser, search } = usePostStore();

  useEffect(() => {
    if (tagsFilter) {
      search(tagsFilter, name);
    } else {
      fetchPostsByUser(name);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [name, tagsFilter]);

  // Base path for this view (without post id) — used by replaceState.
  const basePath = tagsFilter
    ? `/user/${name}/posts/${tagsFilter}`
    : `/user/${name}/posts`;

  function handlePostOpen(id) {
    setExpandedId(id);
    // Silently update the address bar — same trick as Home.jsx so the grid
    // stays mounted and keeps its virtualised scroll state.
    window.history.replaceState(null, "", `${basePath}/${id}`);
  }

  function handlePostClose() {
    setExpandedId(null);
    window.history.replaceState(null, "", basePath);
  }

  return (
    <div className="flex flex-col" style={{ height: "calc(100vh - 3rem)" }}>
      {/* Breadcrumb */}
      <div
        className="flex items-center gap-1.5 border-b px-3 py-2 text-sm"
        style={{ borderColor: "var(--color-border)" }}
      >
        <Link
          to={`/user/${name}`}
          className="hover:opacity-80"
          style={{ color: "var(--color-accent)" }}
        >
          {name}
        </Link>
        <span style={{ color: "var(--color-border)" }}>/</span>
        {tagsFilter ? (
          <>
            <Link
              to={`/user/${name}/posts`}
              className="hover:opacity-80"
              style={{ color: "var(--color-muted)" }}
            >
              posts
            </Link>
            <span style={{ color: "var(--color-border)" }}>/</span>
            <span style={{ color: "var(--color-text)" }}>{tagsFilter}</span>
          </>
        ) : (
          <span style={{ color: "var(--color-text)" }}>posts</span>
        )}
      </div>

      {listError && (
        <p className="px-3 pt-2 pb-1 text-sm text-red-400">{listError}</p>
      )}

      <PostGrid
        initialExpanded={initialExpandedId}
        expandedId={expandedId}
        setExpandedId={(id) => {
          if (id == null) handlePostClose();
          else handlePostOpen(id);
        }}
        onPostOpen={handlePostOpen}
        onPostClose={handlePostClose}
      />
    </div>
  );
}
