/**
 * VoteButtons — generic ±1 / 0 voting widget.
 *
 * Props:
 *   score     number   current score to display
 *   vote      number   current user's vote: 1, -1, or 0
 *   onVote    fn(v)    called with 1, -1, or 0 (toggle off = 0)
 *   disabled  bool     grey out while loading / not logged in
 */
export default function VoteButtons({ score = 0, vote = 0, onVote, disabled }) {
  const isUp = vote === 1;
  const isDown = vote === -1;

  function cast(v) {
    if (disabled || !onVote) return;
    // clicking the active arrow removes the vote
    onVote(vote === v ? 0 : v);
  }

  return (
    <div className="flex flex-col items-center gap-0.5 select-none">
      <button
        onClick={() => cast(1)}
        disabled={disabled}
        aria-label="upvote"
        className={`text-base leading-none transition-colors ${
          isUp
            ? "text-(--color-accent)"
            : "text-(--color-muted) hover:text-(--color-text)"
        } disabled:opacity-40`}
      >
        ▲
      </button>
      <span
        className={`text-xs font-mono tabular-nums ${
          isUp
            ? "text-(--color-accent)"
            : isDown
              ? "text-blue-400"
              : "text-(--color-muted)"
        }`}
      >
        {score}
      </span>
      <button
        onClick={() => cast(-1)}
        disabled={disabled}
        aria-label="downvote"
        className={`text-base leading-none transition-colors ${
          isDown
            ? "text-blue-400"
            : "text-(--color-muted) hover:text-(--color-text)"
        } disabled:opacity-40`}
      >
        ▼
      </button>
    </div>
  );
}
