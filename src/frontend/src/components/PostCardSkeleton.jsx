/** Pulsing placeholder shown while a grid cell is loading. */
export default function PostCardSkeleton() {
  return (
    <article className="overflow-hidden rounded-[var(--radius-card)] border border-(--color-border) bg-(--color-surface) animate-pulse">
      <div className="aspect-square w-full bg-(--color-border)" />
    </article>
  );
}
