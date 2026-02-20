import { useEffect } from "react";
import { useParams, Link } from "react-router-dom";
import VoteButtons from "../components/VoteButtons.jsx";
import TagChip from "../components/TagChip.jsx";
import CommentItem from "../components/CommentItem.jsx";
import CommentForm from "../components/CommentForm.jsx";
import useAuthStore from "../stores/authStore.js";
import usePostStore from "../stores/postStore.js";
import useCommentStore from "../stores/commentStore.js";
import useTagStore from "../stores/tagStore.js";

export default function Post() {
  const { id } = useParams();
  const postId = Number(id);

  const user = useAuthStore((s) => s.user);
  const { current, postLoading, postError, fetchPost, votePost } = usePostStore();
  const seedComments = useCommentStore((s) => s.seed);
  const comments = useCommentStore((s) => s.byPost[postId] ?? null);
  const seedTags = useTagStore((s) => s.seed);
  const tags = useTagStore((s) => s.byPost[postId] ?? []);
  const voteTag = useTagStore((s) => s.voteTag);

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

  if (postLoading)
    return <main className="p-4 text-sm text-(--color-muted)">loading…</main>;
  if (postError)
    return <main className="p-4 text-sm text-red-400">Error: {postError}</main>;
  if (!current) return null;

  const post = current.data;
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
            <video src={mediaSrc} controls className="mx-auto max-h-[80vh] w-full object-contain" />
          ) : (
            <img src={mediaSrc} alt="" className="mx-auto max-h-[80vh] w-full object-contain" />
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
            <Link to={`/user/${post.user_name}`} className="text-(--color-text) hover:text-(--color-accent)">
              {post.user_name}
            </Link>
          </p>
          {tags.length > 0 && (
            <div className="mt-2 flex flex-wrap gap-1">
              {tags.map((t) => (
                <TagChip
                  key={t.id}
                  tag={t}
                  onVote={user ? (tagId, v) => voteTag(postId, tagId, v) : undefined}
                />
              ))}
            </div>
          )}
        </div>
      </div>

      {/* Comments */}
      <section>
        <h2 className="mb-2 text-sm font-semibold text-(--color-muted) uppercase tracking-wide">
          comments{comments ? ` (${comments.length})` : ""}
        </h2>
        <CommentForm postId={postId} />
        {comments && comments.map((c) => (
          <CommentItem key={c.id} comment={c} postId={postId} />
        ))}
      </section>
    </main>
  );
}
