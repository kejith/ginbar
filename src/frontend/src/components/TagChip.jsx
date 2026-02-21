import { useNavigate } from "react-router-dom";

/**
 * TagChip — clickable tag that navigates to /?q=name.
 * Optionally shows a vote widget if onVote is provided.
 *
 * Props:
 *   tag     { id, name, score, vote }
 *   onVote  fn(tagId, voteState)  optional
 */
export default function TagChip({ tag, onVote }) {
  const navigate = useNavigate();
  const name = typeof tag.name === "object" ? tag.name.String : tag.name;

  return (
    <span className="inline-flex items-center gap-1 rounded-full border border-(--color-border) bg-(--color-surface) px-2 py-0.5 text-xs">
      <button
        onClick={() => navigate(`/?q=${encodeURIComponent(name)}`)}
        className="text-(--color-text) hover:text-(--color-accent) truncate max-w-[12ch]"
        title={name}
      >
        {name}
      </button>
      <span className="text-(--color-muted) tabular-nums font-mono">
        {tag.score}
      </span>
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
            className={`text-[10px] leading-none ${tag.vote === -1 ? "text-blue-400" : "text-(--color-muted) hover:text-(--color-text)"}`}
          >
            −
          </button>
        </span>
      )}
    </span>
  );
}
