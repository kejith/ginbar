import { useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import PostGrid from "../components/PostGrid.jsx";
import usePostStore from "../stores/postStore.js";

export default function Home() {
  const [searchParams, setSearchParams] = useSearchParams();
  const query = searchParams.get("q") || "";
  const tag = searchParams.get("tag") || "";
  const postParam = searchParams.get("post");
  const initialExpanded = postParam ? Number(postParam) : null;

  const [expandedId, setExpandedId] = useState(initialExpanded);

  const { listError, fetchPosts, search } = usePostStore();

  // Initial data load
  useEffect(() => {
    if (query) {
      search(query);
    } else {
      fetchPosts({ page: 1, tag: tag || undefined, reset: true });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, tag]);

  function handlePostOpen(id) {
    setExpandedId(id);
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.set("post", id);
        return next;
      },
      { replace: true },
    );
  }

  function handlePostClose() {
    setExpandedId(null);
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete("post");
        return next;
      },
      { replace: true },
    );
  }

  return (
    <div className="flex flex-col" style={{ height: "calc(100vh - 3rem)" }}>
      {(query || tag) && (
        <p className="px-3 pt-2 pb-1 text-sm text-(--color-muted)">
          {query ? `results for "${query}"` : `tag: ${tag}`}
        </p>
      )}

      {listError && (
        <p className="px-3 pt-2 pb-1 text-sm text-red-400">{listError}</p>
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
      />
    </div>
  );
}
