/**
 * PostCard — thumbnail-only grid cell for the home feed.
 *
 * Props:
 *   post        post object from API
 *   onExpand    (id: number) => void — called when thumbnail is clicked
 *   isExpanded  boolean — highlight ring when this post is expanded
 */
export default function PostCard({ post, onExpand, isExpanded }) {
  const isVideo =
    post.content_type?.startsWith("video/") ||
    post.filename?.match(/\.(mp4|webm|mov)$/i);

  // Prefer thumbnail; fall back to original filename
  const thumb = post.thumbnail_filename || post.filename;
  const thumbSrc = thumb ? `/images/thumbnails/${thumb}` : null;

  // Preload full image on hover so it's in the browser cache before expand
  function handleMouseEnter() {
    if (!isVideo && post.filename) {
      const img = new window.Image();
      img.src = `/images/${post.filename}`;
    }
  }

  function handleThumbClick(e) {
    e.preventDefault();
    if (onExpand) onExpand(post.id);
  }

  return (
    <article
      className={`group relative overflow-hidden rounded-sm border bg-(--color-surface) transition-colors ${
        isExpanded ? "border-(--color-accent)" : "border-(--color-border)"
      }`}
    >
      {/* Thumbnail */}
      <button
        onClick={handleThumbClick}
        onMouseEnter={handleMouseEnter}
        className="block aspect-square w-full overflow-hidden bg-black cursor-pointer"
        aria-label={`View post ${post.id}`}
      >
        {thumbSrc ? (
          isVideo ? (
            <div className="relative h-full w-full">
              <img
                src={thumbSrc}
                alt=""
                loading="lazy"
                decoding="async"
                className="h-full w-full object-cover transition-opacity group-hover:opacity-80"
              />
              <span className="pointer-events-none absolute bottom-1 right-1 rounded bg-black/70 px-1 text-[10px] text-white">
                ▶
              </span>
            </div>
          ) : (
            <img
              src={thumbSrc}
              alt=""
              loading="lazy"
              decoding="async"
              className="h-full w-full object-cover transition-opacity group-hover:opacity-80"
            />
          )
        ) : (
          <div className="flex h-full w-full items-center justify-center text-(--color-muted) text-sm">
            no preview
          </div>
        )}
      </button>
    </article>
  );
}
