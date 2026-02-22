import { useState } from "react";
import VoteButtons from "./VoteButtons.jsx";
import CommentForm from "./CommentForm.jsx";
import UserLink from "./UserLink.jsx";
import useAuthStore from "../stores/authStore.js";
import useCommentStore from "../stores/commentStore.js";

// Each nesting level adds 16 px indent, capped at depth 8 (~128 px).
const INDENT_PX = 16;
const MAX_DEPTH_INDENT = 8;

/**
 * CommentItem — recursive comment node with vote, reply, and delete support.
 *
 * Props:
 *   comment   comment object enriched with a `replies` array
 *   postId    number
 *   depth     number     current nesting depth (default 0)
 *   onDelete  fn(id)     optional — shown only for admins
 *   deleting  number|null  id of comment currently being deleted
 */
export default function CommentItem({
  comment,
  postId,
  depth = 0,
  onDelete,
  deleting,
}) {
  const user = useAuthStore((s) => s.user);
  const voteComment = useCommentStore((s) => s.voteComment);
  const [showReply, setShowReply] = useState(false);

  const ts = comment.created_at
    ? new Date(
        typeof comment.created_at === "object"
          ? comment.created_at.Time
          : comment.created_at,
      ).toLocaleString()
    : "";

  const indentPx = Math.min(depth, MAX_DEPTH_INDENT) * INDENT_PX;

  return (
    <div style={{ marginLeft: indentPx }}>
      <div
        className={depth > 0 ? "border-l-2 border-(--color-border) pl-3" : ""}
      >
        {/* Comment row */}
        <div className="flex gap-3 py-3">
          <VoteButtons
            score={comment.score}
            vote={comment.vote ?? 0}
            onVote={(v) => user && voteComment(postId, comment.id, v)}
            disabled={!user}
          />
          <div className="min-w-0 flex-1">
            <p className="mb-1 text-xs text-(--color-muted)">
              <UserLink name={comment.user_name} /> · {ts}
            </p>
            <p className="text-sm wrap-break-word whitespace-pre-wrap text-(--color-text)">
              {comment.content}
            </p>
            {user && (
              <button
                onClick={() => setShowReply((v) => !v)}
                className="mt-1 text-xs text-(--color-muted) hover:text-(--color-text) transition-colors"
              >
                {showReply ? "cancel" : "reply"}
              </button>
            )}
          </div>
          {onDelete && (
            <button
              disabled={deleting === comment.id}
              onClick={() => onDelete(comment.id)}
              className="self-start mt-1 shrink-0 rounded bg-(--color-danger) px-2 py-0.5 text-xs text-white disabled:opacity-50"
              title="delete comment"
            >
              {deleting === comment.id ? "…" : "del"}
            </button>
          )}
        </div>

        {/* Inline reply form */}
        {showReply && (
          <CommentForm
            postId={postId}
            parentId={comment.id}
            onSubmitSuccess={() => setShowReply(false)}
            onCancel={() => setShowReply(false)}
          />
        )}

        {/* Recursive replies */}
        {(comment.replies ?? []).map((reply) => (
          <CommentItem
            key={reply.id}
            comment={reply}
            postId={postId}
            depth={depth + 1}
            onDelete={onDelete}
            deleting={deleting}
          />
        ))}
      </div>
    </div>
  );
}
