import { Link } from "react-router-dom";
import VoteButtons from "./VoteButtons.jsx";
import TagChip from "./TagChip.jsx";
import useAuthStore from "../stores/authStore.js";
import usePostStore from "../stores/postStore.js";

/**
 * PostCard — grid cell for the home feed.
 *
 * Props:
 *   post   post object from API (Post or GetVotedPostsRow)
 *   tags   optional tag array for this post
 */
export default function PostCard({ post, tags }) {
  const user = useAuthStore((s) => s.user);
  const votePost = usePostStore((s) => s.votePost);

  const isVideo =
    post.content_type?.startsWith("video/") ||
    post.filename?.match(/\.(mp4|webm|mov)$/i);

  // Prefer thumbnail; fall back to original filename
  const thumb = post.thumbnail_filename || post.filename;
  const thumbSrc = thumb ? `/images/thumbnails/${thumb}` : null;

  function handleVote(v) {
    if (!user) return;
    votePost(post.id, v);
  }

  return (
    <article className="group relative flex flex-col overflow-hidden rounded-lg border border-(--color-border) bg-(--color-surface)">
      {/* Thumbnail */}
      <Link to={`/post/${post.id}`} className="block aspect-square overflow-hidden bg-black">
        {thumbSrc ? (
          isVideo ? (
            <div className="relative h-full w-full">
              <img
                src={thumbSrc}
                alt=""
                loading="lazy"
                decoding="async"
                className="h-full w-full object-cover transition-opacity group-hover:opacity-80"
              />
              <span className="pointer-events-none absolute bottom-1 right-1 rounded bg-black/70 px-1 text-[10px] text-white">
                ▶
              </span>
            </div>
          ) : (
            <img
              src={thumbSrc}
              alt=""
              loading="lazy"
              decoding="async"
              className="h-full w-full object-cover transition-opacity group-hover:opacity-80"
            />
          )
        ) : (
          <div className="flex h-full w-full items-center justify-center text-(--color-muted) text-sm">
            no preview
          </div>
        )}
      </Link>

      {/* Footer */}
      <div className="flex items-start gap-2 p-2">
        <VoteButtons
          score={post.score}
          vote={post.vote ?? 0}
          onVote={handleVote}
          disabled={!user}
        />
        <div className="min-w-0 flex-1">
          <p className="truncate text-xs text-(--color-muted)">
            <Link
              to={`/user/${post.user_name}`}
              className="hover:text-(--color-text)"
            >
              {post.user_name}
            </Link>
          </p>
          {/* Tags */}
          {tags && tags.length > 0 && (
            <div className="mt-1 flex flex-wrap gap-1">
              {tags.map((t) => (
                <TagChip key={t.id} tag={t} />
              ))}
            </div>
          )}
        </div>
      </div>
    </article>
  );
}
