import { useEffect } from "react";
import { useParams, Link } from "react-router-dom";
import usePostStore from "../stores/postStore.js";
import useAuthStore from "../stores/authStore.js";
import PostCard from "../components/PostCard.jsx";

export default function Profile() {
  const { name } = useParams();
  const currentUser = useAuthStore((s) => s.user);
  const { posts, listLoading, listError, fetchPostsByUser } = usePostStore();

  useEffect(() => {
    fetchPostsByUser(name);
  }, [name]);

  const isOwn = currentUser && currentUser.name === name;

  return (
    <main className="mx-auto max-w-5xl p-4">
      {/* Header */}
      <div className="mb-6 flex items-center gap-4">
        <div
          className="flex h-14 w-14 shrink-0 items-center justify-center rounded-full text-xl font-bold"
          style={{
            background: "var(--color-surface)",
            color: "var(--color-accent)",
          }}
        >
          {name[0]?.toUpperCase()}
        </div>
        <div>
          <h1 className="text-xl font-bold text-(--color-text)">{name}</h1>
          {isOwn && (
            <span className="text-xs text-(--color-muted)">
              level {currentUser.level}
            </span>
          )}
        </div>
      </div>

      {/* Posts grid */}
      {listLoading && <p className="text-sm text-(--color-muted)">Loading…</p>}
      {listError && <p className="text-sm text-red-400">{listError}</p>}
      {!listLoading && !listError && posts.length === 0 && (
        <p className="text-sm text-(--color-muted)">No posts yet.</p>
      )}

      <div
        className="grid gap-3"
        style={{ gridTemplateColumns: "repeat(auto-fill, minmax(160px, 1fr))" }}
      >
        {posts.map((post) => (
          <PostCard key={post.id} post={post} />
        ))}
      </div>

      <Link
        to="/"
        className="mt-8 inline-block text-sm text-(--color-muted) underline hover:text-(--color-text)"
      >
        ← back
      </Link>
    </main>
  );
}
