import { useNavigate } from "react-router-dom";

/**
 * TagChip — clickable tag that navigates to /?q=name.
 * Optionally shows a vote widget if onVote is provided.
 * Optionally shows a delete button if onDelete is provided (admin only).
 *
 * Props:
 *   tag       { id, name, score, vote }
 *   onVote    fn(tagId, voteState)  optional
 *   onDelete  fn(tagId)             optional — admin only
 *   deleting  bool                  disables delete button while in flight
 */
export default function TagChip({ tag, onVote, onDelete, deleting }) {
  const navigate = useNavigate();
  const name = typeof tag.name === "object" ? tag.name.String : tag.name;

  return (
    <span className="inline-flex items-center gap-1 rounded-full border border-(--color-border) bg-(--color-surface) px-2 py-0.5 text-xs">
      <button
        onClick={() => navigate(`/?q=${encodeURIComponent(name)}`)}
        className="text-(--color-text) hover:text-(--color-accent) truncate max-w-[14ch]"
        title={name}
      >
        {name}
      </button>
      {onVote && (
        <span className="flex gap-0.5">
          <button
            onClick={() => onVote(tag.id, tag.vote === 1 ? 0 : 1)}
            className={`text-[10px] leading-none ${tag.vote === 1 ? "text-(--color-accent)" : "text-(--color-muted) hover:text-(--color-text)"}`}
          >
            +
          </button>
          <button
            onClick={() => onVote(tag.id, tag.vote === -1 ? 0 : -1)}
            className={`text-[10px] leading-none ${tag.vote === -1 ? "text-(--color-down)" : "text-(--color-muted) hover:text-(--color-text)"}`}
          >
            −
          </button>
        </span>
      )}
      {onDelete && (
        <button
          disabled={deleting}
          onClick={() => onDelete(tag.id)}
          className="text-(--color-danger) hover:opacity-80 disabled:opacity-40 text-[10px] leading-none"
          title="delete tag"
        >
          ×
        </button>
      )}
    </span>
  );
}
