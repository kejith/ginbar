import { useEffect, useRef } from "react";
import { Link } from "react-router-dom";
import VoteButtons from "./VoteButtons.jsx";
import TagChip from "./TagChip.jsx";
import CommentItem from "./CommentItem.jsx";
import CommentForm from "./CommentForm.jsx";
import useAuthStore from "../stores/authStore.js";
import usePostStore from "../stores/postStore.js";
import useCommentStore from "../stores/commentStore.js";
import useTagStore from "../stores/tagStore.js";

/**
 * InlinePost — full post view embedded between grid rows.
 *
 * Props:
 *   postId      number — id of the post to show
 *   onClose     () => void
 *   onNewer     () => void — navigate to next newer post (left arrow)
 *   onOlder     () => void — navigate to next older post (right arrow)
 *   canGoNewer  boolean
 *   canGoOlder  boolean
 */
export default function InlinePost({
  postId,
  onClose,
  onNewer,
  onOlder,
  canGoNewer,
  canGoOlder,
}) {
  const panelRef = useRef(null);

  const user = useAuthStore((s) => s.user);
  const { current, postLoading, postError, fetchPost, votePost } =
    usePostStore();
  const seedComments = useCommentStore((s) => s.seed);
  const comments = useCommentStore((s) => s.byPost[postId]);
  const seedTags = useTagStore((s) => s.seed);
  const tags = useTagStore((s) => s.byPost[postId]);
  const voteTag = useTagStore((s) => s.voteTag);

  // Fetch whenever postId changes
  useEffect(() => {
    fetchPost(postId);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [postId]);

  // Seed sub-stores when data arrives
  useEffect(() => {
    if (current && current.data?.id === postId) {
      seedComments(postId, current.comments);
      seedTags(postId, current.tags);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [current, postId]);

  // Keyboard navigation
  useEffect(() => {
    function onKey(e) {
      if (e.key === "ArrowLeft" && canGoNewer) onNewer();
      if (e.key === "ArrowRight" && canGoOlder) onOlder();
      if (e.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [canGoNewer, canGoOlder, onNewer, onOlder, onClose]);

  const isReady =
    !postLoading && !postError && current && current.data?.id === postId;
  const post = isReady ? current.data : null;
  const tagList = tags ?? [];
  const commentList = comments ?? [];

  const isVideo =
    post?.content_type?.startsWith("video/") ||
    post?.filename?.match(/\.(mp4|webm|mov)$/i);
  const mediaSrc = post?.filename
    ? isVideo
      ? `/videos/${post.filename}`
      : `/images/${post.filename}`
    : null;

  return (
    <div
      ref={panelRef}
      className="w-full border-t border-b border-(--color-border) bg-(--color-surface)"
    >
      {/* Toolbar */}
      <div className="flex items-center justify-between px-4 py-2 border-b border-(--color-border)">
        {/* Left arrow — newer post */}
        <button
          onClick={onNewer}
          disabled={!canGoNewer}
          title="Newer post"
          className="flex items-center gap-1 rounded px-3 py-1.5 text-sm text-(--color-muted) hover:text-(--color-text) disabled:opacity-30 disabled:cursor-not-allowed transition-opacity"
        >
          ← newer
        </button>

        {/* Close */}
        <button
          onClick={onClose}
          title="Close"
          className="rounded px-3 py-1.5 text-sm text-(--color-muted) hover:text-(--color-text)"
        >
          ✕ close
        </button>

        {/* Right arrow — older post */}
        <button
          onClick={onOlder}
          disabled={!canGoOlder}
          title="Older post"
          className="flex items-center gap-1 rounded px-3 py-1.5 text-sm text-(--color-muted) hover:text-(--color-text) disabled:opacity-30 disabled:cursor-not-allowed transition-opacity"
        >
          older →
        </button>
      </div>

      {/* Content */}
      {postLoading && (
        <div className="flex h-64 items-center justify-center text-sm text-(--color-muted) animate-pulse">
          loading…
        </div>
      )}

      {postError && (
        <div className="flex h-32 items-center justify-center text-sm text-red-400">
          Error: {postError}
        </div>
      )}

      {isReady && (
        <div className="mx-auto max-w-3xl p-4">
          {/* Media */}
          <div className="mb-4 overflow-hidden rounded-lg bg-black">
            {mediaSrc ? (
              isVideo ? (
                <video
                  key={mediaSrc}
                  src={mediaSrc}
                  controls
                  className="mx-auto max-h-[70vh] w-full object-contain"
                />
              ) : (
                <img
                  key={mediaSrc}
                  src={mediaSrc}
                  alt=""
                  className="mx-auto max-h-[70vh] w-full object-contain"
                />
              )
            ) : (
              <div className="flex h-48 items-center justify-center text-sm text-(--color-muted)">
                no media
              </div>
            )}
          </div>

          {/* Meta */}
          <div className="mb-4 flex items-start gap-3">
            <VoteButtons
              score={post.score}
              vote={post.vote ?? 0}
              onVote={(v) => user && votePost(postId, v)}
              disabled={!user}
            />
            <div className="min-w-0 flex-1">
              <p className="text-xs text-(--color-muted)">
                posted by{" "}
                <Link
                  to={`/user/${post.user_name}`}
                  className="text-(--color-text) hover:text-(--color-accent)"
                >
                  {post.user_name}
                </Link>
                <Link
                  to={`/post/${post.id}`}
                  className="ml-3 text-(--color-muted) hover:text-(--color-accent)"
                  title="Open full page"
                >
                  ↗ permalink
                </Link>
              </p>
              {tagList.length > 0 && (
                <div className="mt-2 flex flex-wrap gap-1">
                  {tagList.map((t) => (
                    <TagChip
                      key={t.id}
                      tag={t}
                      onVote={
                        user
                          ? (tagId, v) => voteTag(postId, tagId, v)
                          : undefined
                      }
                    />
                  ))}
                </div>
              )}
            </div>
          </div>

          {/* Comments */}
          <section>
            <h2 className="mb-2 text-sm font-semibold text-(--color-muted) uppercase tracking-wide">
              comments
              {commentList.length > 0 ? ` (${commentList.length})` : ""}
            </h2>
            <CommentForm postId={postId} />
            {commentList.map((c) => (
              <CommentItem key={c.id} comment={c} postId={postId} />
            ))}
          </section>
        </div>
      )}
    </div>
  );
}
