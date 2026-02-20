import { useEffect } from "react";
import { useSearchParams } from "react-router-dom";
import PostCard from "../components/PostCard.jsx";
import usePostStore from "../stores/postStore.js";

export default function Home() {
  const [searchParams] = useSearchParams();
  const query = searchParams.get("q") || "";
  const tag = searchParams.get("tag") || "";

  const { posts, page, hasMore, listLoading, listError, fetchPosts, search } =
    usePostStore();

  useEffect(() => {
    if (query) {
      search(query);
    } else {
      fetchPosts({ page: 1, tag: tag || undefined, reset: true });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, tag]);

  function loadMore() {
    if (query) return;
    fetchPosts({ page: page + 1, tag: tag || undefined });
  }

  return (
    <main className="p-3">
      {(query || tag) && (
        <p className="mb-3 text-sm text-(--color-muted)">
          {query ? `results for "${query}"` : `tag: ${tag}`}
        </p>
      )}

      {listError && <p className="mb-3 text-sm text-red-400">{listError}</p>}

      <div
        className="grid gap-2"
        style={{ gridTemplateColumns: "repeat(auto-fill, minmax(180px, 1fr))" }}
      >
        {posts.map((post) => (
          <PostCard key={post.id} post={post} />
        ))}
      </div>

      {!listLoading && posts.length === 0 && (
        <p className="mt-12 text-center text-sm text-(--color-muted)">
          nothing here yet
        </p>
      )}

      {listLoading && (
        <p className="mt-6 text-center text-sm text-(--color-muted)">
          loading…
        </p>
      )}

      {!listLoading && hasMore && !query && (
        <div className="mt-6 flex justify-center">
          <button
            onClick={loadMore}
            className="rounded border border-(--color-border) px-5 py-2 text-sm text-(--color-muted) hover:text-(--color-text)"
          >
            load more
          </button>
        </div>
      )}
    </main>
  );
}
