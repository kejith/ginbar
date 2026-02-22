import { useState } from "react";
import useAuthStore from "../stores/authStore.js";
import useCommentStore from "../stores/commentStore.js";

/**
 * CommentForm — textarea + submit to add a comment.
 *
 * Props:
 *   postId          number
 *   parentId        number | null   — set when replying to another comment
 *   onSubmitSuccess fn()            — called after a successful submit
 *   onCancel        fn()            — optional cancel button
 */
export default function CommentForm({
  postId,
  parentId = null,
  onSubmitSuccess,
  onCancel,
}) {
  const user = useAuthStore((s) => s.user);
  const { createComment, loading, error } = useCommentStore();
  const [text, setText] = useState("");

  if (!user) {
    return (
      <p className="py-3 text-sm text-(--color-muted)">
        <a href="/login" className="underline hover:text-(--color-text)">
          Log in
        </a>{" "}
        to comment.
      </p>
    );
  }

  async function submit(e) {
    e.preventDefault();
    const content = text.trim();
    if (!content) return;
    await createComment(postId, content, parentId);
    setText("");
    onSubmitSuccess?.();
  }

  return (
    <form onSubmit={submit} className="py-2">
      <textarea
        value={text}
        onChange={(e) => setText(e.target.value)}
        rows={3}
        placeholder={parentId ? "Write a reply…" : "Write a comment…"}
        className="w-full rounded-[var(--radius-sm)] bg-(--color-bg) p-2 text-sm text-(--color-text) placeholder:text-(--color-muted) ring-1 ring-(--color-border) focus:outline-none focus:ring-(--color-accent) resize-none"
      />
      {error && <p className="mt-1 text-xs text-(--color-danger)">{error}</p>}
      <div className="mt-2 flex gap-2 justify-end">
        {onCancel && (
          <button
            type="button"
            onClick={onCancel}
            className="rounded-[var(--radius-sm)] px-4 py-1.5 text-sm font-medium text-(--color-muted) hover:text-(--color-text)"
          >
            cancel
          </button>
        )}
        <button
          type="submit"
          disabled={loading || !text.trim()}
          className="rounded-[var(--radius-sm)] bg-(--color-accent) px-4 py-1.5 text-sm font-medium text-(--color-accent-text) disabled:opacity-50"
        >
          {loading ? "posting…" : parentId ? "reply" : "post"}
        </button>
      </div>
    </form>
  );
}
