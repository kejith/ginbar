import { useState } from "react";
import useAuthStore from "../stores/authStore.js";
import useCommentStore from "../stores/commentStore.js";

/**
 * CommentForm — textarea + submit to add a comment.
 *
 * Props:
 *   postId  number
 */
export default function CommentForm({ postId }) {
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
    await createComment(postId, content);
    setText("");
  }

  return (
    <form onSubmit={submit} className="py-3">
      <textarea
        value={text}
        onChange={(e) => setText(e.target.value)}
        rows={3}
        placeholder="Write a comment…"
        className="w-full rounded bg-(--color-bg) p-2 text-sm text-(--color-text) placeholder:text-(--color-muted) ring-1 ring-(--color-border) focus:outline-none focus:ring-(--color-accent) resize-none"
      />
      {error && <p className="mt-1 text-xs text-red-400">{error}</p>}
      <div className="mt-2 flex justify-end">
        <button
          type="submit"
          disabled={loading || !text.trim()}
          className="rounded bg-(--color-accent) px-4 py-1.5 text-sm font-medium text-white disabled:opacity-50"
        >
          {loading ? "posting…" : "post"}
        </button>
      </div>
    </form>
  );
}
