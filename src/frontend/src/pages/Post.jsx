import { useEffect, useState } from "react";
import { useParams, Link, useNavigate } from "react-router-dom";
import VoteButtons from "../components/VoteButtons.jsx";
import TagChip from "../components/TagChip.jsx";
import CommentItem from "../components/CommentItem.jsx";
import CommentForm from "../components/CommentForm.jsx";
import useAuthStore from "../stores/authStore.js";
import usePostStore from "../stores/postStore.js";
import useCommentStore from "../stores/commentStore.js";
import useTagStore from "../stores/tagStore.js";
import { isAdmin } from "../utils/roles.js";
import api from "../utils/api.js";

export default function Post() {
  const { id } = useParams();
  const postId = Number(id);
  const navigate = useNavigate();

  const user = useAuthStore((s) => s.user);
  const admin = isAdmin(user);
  const { current, postLoading, postError, fetchPost, votePost } =
    usePostStore();
  const seedComments = useCommentStore((s) => s.seed);
  const comments = useCommentStore((s) => s.byPost[postId]);
  const seedTags = useTagStore((s) => s.seed);
  const tags = useTagStore((s) => s.byPost[postId]);
  const voteTag = useTagStore((s) => s.voteTag);
  const [deletingPost, setDeletingPost] = useState(false);
  const [deletingComment, setDeletingComment] = useState(null);
  const [deletingTag, setDeletingTag] = useState(null);

  useEffect(() => {
    fetchPost(postId);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [postId]);

  useEffect(() => {
    if (current) {
      seedComments(postId, current.comments);
      seedTags(postId, current.tags);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [current]);

  async function handleDeletePost() {
    if (!confirm(`Delete post #${postId}? This cannot be undone.`)) return;
    setDeletingPost(true);
    try {
      await api.delete(`/admin/posts/${postId}`);
      navigate("/");
    } catch (e) {
      alert(e.message);
      setDeletingPost(false);
    }
  }

  async function handleDeleteComment(commentId) {
    if (!confirm(`Delete comment #${commentId}?`)) return;
    setDeletingComment(commentId);
    try {
      await api.delete(`/admin/comments/${commentId}`);
      seedComments(
        postId,
        (comments ?? []).filter((c) => c.id !== commentId),
      );
    } catch (e) {
      alert(e.message);
    } finally {
      setDeletingComment(null);
    }
  }

  async function handleDeleteTag(tagId) {
    if (!confirm(`Delete tag #${tagId}?`)) return;
    setDeletingTag(tagId);
    try {
      await api.delete(`/admin/tags/${tagId}`);
      seedTags(
        postId,
        (tags ?? []).filter((t) => t.id !== tagId),
      );
    } catch (e) {
      alert(e.message);
    } finally {
      setDeletingTag(null);
    }
  }

  if (postLoading)
    return <main className="p-4 text-sm text-(--color-muted)">loading…</main>;
  if (postError)
    return <main className="p-4 text-sm text-red-400">Error: {postError}</main>;
  if (!current) return null;

  const post = current.data;
  const tagList = tags ?? [];
  const commentList = comments ?? [];
  const isVideo =
    post.content_type?.startsWith("video/") ||
    post.filename?.match(/\.(mp4|webm|mov)$/i);
  const mediaSrc = post.filename
    ? isVideo
      ? `/videos/${post.filename}`
      : `/images/${post.filename}`
    : null;

  return (
    <main className="mx-auto max-w-3xl p-4">
      {/* Media */}
      <div className="mb-4 overflow-hidden rounded-lg bg-black">
        {mediaSrc ? (
          isVideo ? (
            <video
              src={mediaSrc}
              controls
              className="mx-auto max-h-[80vh] w-full object-contain"
            />
          ) : (
            <img
              src={mediaSrc}
              alt=""
              className="mx-auto max-h-[80vh] w-full object-contain"
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
          <div className="flex items-center gap-2">
            <p className="text-xs text-(--color-muted)">
              posted by{" "}
              <Link
                to={`/user/${post.user_name}`}
                className="text-(--color-text) hover:text-(--color-accent)"
              >
                {post.user_name}
              </Link>
            </p>
            {admin && (
              <button
                disabled={deletingPost}
                onClick={handleDeletePost}
                className="rounded bg-red-700 px-2 py-0.5 text-xs text-white disabled:opacity-50"
              >
                {deletingPost ? "deleting…" : "delete post"}
              </button>
            )}
          </div>
          {tagList.length > 0 && (
            <div className="mt-2 flex flex-wrap gap-1">
              {tagList.map((t) => (
                <TagChip
                  key={t.id}
                  tag={t}
                  onVote={
                    user ? (tagId, v) => voteTag(postId, tagId, v) : undefined
                  }
                  onDelete={
                    admin ? (tagId) => handleDeleteTag(tagId) : undefined
                  }
                  deleting={deletingTag === t.id}
                />
              ))}
            </div>
          )}
        </div>
      </div>

      {/* Comments */}
      <section>
        <h2 className="mb-2 text-sm font-semibold text-(--color-muted) uppercase tracking-wide">
          comments{commentList.length > 0 ? ` (${commentList.length})` : ""}
        </h2>
        <CommentForm postId={postId} />
        {commentList.map((c) => (
          <CommentItem
            key={c.id}
            comment={c}
            postId={postId}
            onDelete={admin ? () => handleDeleteComment(c.id) : undefined}
            deleting={deletingComment === c.id}
          />
        ))}
      </section>
    </main>
  );
}
