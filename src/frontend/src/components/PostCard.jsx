import { useState } from "react";
import useAuthStore from "../stores/authStore.js";
import usePostStore from "../stores/postStore.js";
import { isAdmin } from "../utils/roles.js";
import api from "../utils/api.js";

/**
 * PostCard — thumbnail-only grid cell for the home feed.
 *
 * Props:
 *   post        post object from API
 *   onExpand    (id: number) => void — called when thumbnail is clicked
 *   isExpanded  boolean — highlight ring when this post is expanded
 */
export default function PostCard({ post, onExpand, isExpanded }) {
  const user = useAuthStore((s) => s.user);
  const removePost = usePostStore((s) => s.removePost);
  const admin = isAdmin(user);
  const [deleting, setDeleting] = useState(false);

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

  async function handleDelete(e) {
    e.stopPropagation();
    if (deleting) return;
    setDeleting(true);
    try {
      await api.delete(`/admin/posts/${post.id}`);
      removePost(post.id);
    } catch (err) {
      console.error("delete post failed", err);
      setDeleting(false);
    }
  }

  return (
    <article
      className={`group relative overflow-hidden rounded-[var(--radius-card)] border bg-(--color-surface) transition-colors ${
        isExpanded ? "border-(--color-accent)" : "border-(--color-border)"
      }`}
    >
      {/* Thumbnail */}
      <button
        onClick={handleThumbClick}
        onMouseEnter={handleMouseEnter}
        className="post-thumb block aspect-square w-full overflow-hidden bg-black cursor-pointer"
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
                className="h-full w-full object-cover"
              />
              <span className="pointer-events-none absolute bottom-1 right-1 rounded-[var(--radius-sm)] bg-black/70 px-1 text-[10px] text-white">
                ▶
              </span>
            </div>
          ) : (
            <img
              src={thumbSrc}
              alt=""
              loading="lazy"
              decoding="async"
              className="h-full w-full object-cover"
            />
          )
        ) : (
          <div className="flex h-full w-full items-center justify-center text-(--color-muted) text-sm">
            no preview
          </div>
        )}
      </button>

      {/* Admin delete overlay */}
      {admin && (
        <button
          onClick={handleDelete}
          disabled={deleting}
          className="absolute top-1 right-1 z-10 rounded-[var(--radius-sm)] bg-(--color-danger)/80 px-1.5 py-0.5 text-[10px] text-white opacity-0 transition-opacity group-hover:opacity-100 hover:bg-(--color-danger) disabled:cursor-wait"
          aria-label="Delete post"
        >
          {deleting ? "…" : "×"}
        </button>
      )}
    </article>
  );
}
