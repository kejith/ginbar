import VoteButtons from "./VoteButtons.jsx";
import useAuthStore from "../stores/authStore.js";
import useCommentStore from "../stores/commentStore.js";

/**
 * CommentItem — single comment row with vote buttons.
 *
 * Props:
 *   comment   comment object (Comment or GetVotedCommentsRow)
 *   postId    number   needed so the store can find the right list
 *   onDelete  fn()     optional — shown only for admins
 *   deleting  bool     disables button while in flight
 */
export default function CommentItem({ comment, postId, onDelete, deleting }) {
  const user = useAuthStore((s) => s.user);
  const voteComment = useCommentStore((s) => s.voteComment);

  const ts = comment.created_at
    ? new Date(
        typeof comment.created_at === "object"
          ? comment.created_at.Time
          : comment.created_at,
      ).toLocaleString()
    : "";

  return (
    <div className="flex gap-3 border-b border-(--color-border) py-3 last:border-none">
      <VoteButtons
        score={comment.score}
        vote={comment.vote ?? 0}
        onVote={(v) => user && voteComment(postId, comment.id, v)}
        disabled={!user}
      />
      <div className="min-w-0 flex-1">
        <p className="mb-1 text-xs text-(--color-muted)">
          <span className="text-(--color-text) font-medium">
            {comment.user_name}
          </span>{" "}
          · {ts}
        </p>
        <p className="text-sm break-words whitespace-pre-wrap text-(--color-text)">
          {comment.content}
        </p>
      </div>
      {onDelete && (
        <button
          disabled={deleting}
          onClick={onDelete}
          className="self-start mt-1 shrink-0 rounded bg-red-700 px-2 py-0.5 text-xs text-white disabled:opacity-50"
          title="delete comment"
        >
          {deleting ? "…" : "del"}
        </button>
      )}
    </div>
  );
}
