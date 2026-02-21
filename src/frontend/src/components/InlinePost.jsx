import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import CommentItem from "./CommentItem.jsx";
import CommentForm from "./CommentForm.jsx";
import useAuthStore from "../stores/authStore.js";
import usePostStore from "../stores/postStore.js";
import useCommentStore from "../stores/commentStore.js";
import useTagStore from "../stores/tagStore.js";
import { timeAgo } from "../utils/timeAgo.js";

const TOP_TAGS = 5;

/**
 * InlinePost — full post view embedded between grid rows.
 *
 * Props:
 *   postId      number — id of the post to show
 *   listPost    object|null — lightweight post from the grid list (has filename,
 *               thumbnail_filename, content_type). Used to show a thumbnail
 *               placeholder and eagerly preload the full image before the
 *               single-post API fetch completes.
 *   onClose     () => void
 *   onNewer     () => void — navigate to next newer post (left arrow)
 *   onOlder     () => void — navigate to next older post (right arrow)
 *   canGoNewer  boolean
 *   canGoOlder  boolean
 */
export default function InlinePost({
  postId,
  listPost = null,
  onClose,
  onNewer,
  onOlder,
  canGoNewer,
  canGoOlder,
}) {
  const panelRef = useRef(null);
  const tagInputRef = useRef(null);
  const [showAllTags, setShowAllTags] = useState(false);
  const [addingTag, setAddingTag] = useState(false);
  const [tagInput, setTagInput] = useState("");
  const [tagError, setTagError] = useState("");
  const [copied, setCopied] = useState(false);
  // true once the full-resolution image has fired its onLoad event
  const [imgReady, setImgReady] = useState(false);

  const user = useAuthStore((s) => s.user);
  const { current, postLoading, postError, fetchPost, votePost } =
    usePostStore();
  const seedComments = useCommentStore((s) => s.seed);
  const comments = useCommentStore((s) => s.byPost[postId]);
  const seedTags = useTagStore((s) => s.seed);
  const tags = useTagStore((s) => s.byPost[postId]);
  const voteTag = useTagStore((s) => s.voteTag);
  const createTag = useTagStore((s) => s.createTag);

  // Reset tag UI when post changes
  useEffect(() => {
    setShowAllTags(false);
    setAddingTag(false);
    setTagInput("");
    setTagError("");
    setImgReady(false);
  }, [postId]);

  useEffect(() => {
    fetchPost(postId);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [postId]);

  // Eagerly preload the full image as soon as we have a filename from the list
  // post, which arrives long before the single-post API fetch completes.
  useEffect(() => {
    const src = listPost?.filename;
    if (!src) return;
    const isVid =
      listPost?.content_type?.startsWith("video/") ||
      src.match(/\.(mp4|webm|mov)$/i);
    if (isVid) return;
    const img = new window.Image();
    img.src = `/images/${src}`;
    img.onload = () => setImgReady(true);
    return () => {
      img.onload = null;
    };
    // Re-run whenever the post changes so we always preload the right image
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [postId]);

  useEffect(() => {
    if (current && current.data?.id === postId) {
      seedComments(postId, current.comments);
      seedTags(postId, current.tags);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [current, postId]);

  // Focus tag input when opened
  useEffect(() => {
    if (addingTag && tagInputRef.current) tagInputRef.current.focus();
  }, [addingTag]);

  // Keyboard navigation
  useEffect(() => {
    function onKey(e) {
      if (e.key === "ArrowLeft" && canGoNewer) onNewer();
      if (e.key === "ArrowRight" && canGoOlder) onOlder();
      if (e.key === "Escape") {
        if (addingTag) {
          setAddingTag(false);
          return;
        }
        onClose();
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [canGoNewer, canGoOlder, onNewer, onOlder, onClose, addingTag]);

  const isReady =
    !postLoading && !postError && current && current.data?.id === postId;
  const post = isReady ? current.data : null;

  const isVideo =
    post?.content_type?.startsWith("video/") ||
    post?.filename?.match(/\.(mp4|webm|mov)$/i);
  const mediaSrc = post?.filename
    ? isVideo
      ? `/videos/${post.filename}`
      : `/images/${post.filename}`
    : null;

  // Thumbnail placeholder data from the lightweight list post
  const listIsVideo =
    listPost?.content_type?.startsWith("video/") ||
    listPost?.filename?.match(/\.(mp4|webm|mov)$/i);
  const thumbFilename = listPost?.thumbnail_filename || listPost?.filename;
  const thumbUrl = thumbFilename ? `/images/thumbnails/${thumbFilename}` : null;

  const sortedTags = [...(tags ?? [])].sort((a, b) => b.score - a.score);
  const visibleTags = showAllTags ? sortedTags : sortedTags.slice(0, TOP_TAGS);
  const hiddenCount = sortedTags.length - TOP_TAGS;

  const uploadedAt = post?.created_at?.Time ?? post?.created_at;
  const timeStr = timeAgo(uploadedAt);

  async function handleAddTag(e) {
    e.preventDefault();
    const name = tagInput.trim();
    if (!name) return;
    setTagError("");
    try {
      await createTag(postId, name);
      setTagInput("");
      setAddingTag(false);
    } catch (err) {
      setTagError(err.message ?? "failed");
    }
  }

  function handleShare() {
    const url = `${window.location.origin}/?post=${postId}`;
    navigator.clipboard.writeText(url).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  }

  return (
    <div
      ref={panelRef}
      className="w-full border-t border-b border-(--color-border) bg-(--color-surface)"
    >
      {/* Nav bar */}
      <div className="flex items-center justify-between border-b border-(--color-border) px-3 py-1">
        <button
          onClick={onNewer}
          disabled={!canGoNewer}
          className="rounded px-2 py-1 text-sm text-(--color-muted) hover:text-(--color-text) disabled:opacity-30 disabled:cursor-not-allowed"
        >
          ← newer
        </button>
        <button
          onClick={onClose}
          className="rounded px-3 py-1 text-sm text-(--color-muted) hover:text-(--color-text)"
        >
          ✕
        </button>
        <button
          onClick={onOlder}
          disabled={!canGoOlder}
          className="rounded px-2 py-1 text-sm text-(--color-muted) hover:text-(--color-text) disabled:opacity-30 disabled:cursor-not-allowed"
        >
          older →
        </button>
      </div>

      {postError && (
        <div className="flex h-32 items-center justify-center text-sm text-red-400">
          {postError}
        </div>
      )}

      {!postError && (
        <div className="mx-auto max-w-3xl px-3 py-4 space-y-3">
          {/* ── Media ── Always rendered so the thumbnail can show immediately
               and the full image can be preloaded before the fetch completes. */}
          <div className="overflow-hidden rounded-lg bg-black relative">
            {/* Blurred thumbnail placeholder — visible until the full image is
                 ready. Provides instant visual feedback on expand, prevents
                 the blank-then-flash effect. */}
            {!imgReady && !listIsVideo && thumbUrl && (
              <img
                src={thumbUrl}
                alt=""
                aria-hidden
                className="mx-auto w-full object-contain opacity-50 blur-sm"
              />
            )}

            {/* Fallback spinner when there is absolutely no thumbnail to show */}
            {postLoading && !thumbUrl && (
              <div className="flex h-64 items-center justify-center text-sm text-(--color-muted) animate-pulse">
                loading…
              </div>
            )}

            {/* Full-resolution image — positioned off-flow while loading so
                 the thumbnail determines the container height. Fades in once
                 its onLoad fires (which may be immediate if the browser cached
                 it from the eager-preload effect above). */}
            {isReady && !isVideo && mediaSrc && (
              <img
                key={mediaSrc}
                src={mediaSrc}
                alt=""
                onLoad={() => setImgReady(true)}
                className={`mx-auto w-full object-contain transition-opacity duration-300 ${
                  imgReady ? "block opacity-100" : "absolute inset-0 opacity-0"
                }`}
              />
            )}

            {/* Video */}
            {isReady && isVideo && mediaSrc && (
              <video
                key={mediaSrc}
                src={mediaSrc}
                controls
                className="mx-auto w-full object-contain"
              />
            )}

            {/* No media at all */}
            {isReady && !mediaSrc && (
              <div className="flex h-48 items-center justify-center text-sm text-(--color-muted)">
                no media
              </div>
            )}
          </div>

          {/* ── Controls + comments — only once the post data is loaded ── */}
          {isReady && (
            <>
              {/* ── Controls panel ── */}
              <div className="rounded-lg border border-(--color-border) bg-(--color-bg) divide-y divide-(--color-border)">
                {/* Row 1: votes + meta */}
                <div className="flex items-center gap-4 px-4 py-3">
                  {/* Vote buttons */}
                  <div className="flex items-center gap-2 shrink-0">
                    <button
                      onClick={() =>
                        user && votePost(postId, post.vote === 1 ? 0 : 1)
                      }
                      disabled={!user}
                      title="Upvote"
                      className={`flex h-7 w-7 items-center justify-center rounded-full border text-sm font-bold leading-none transition-colors disabled:opacity-40 ${
                        post.vote === 1
                          ? "border-(--color-accent) text-(--color-accent)"
                          : "border-(--color-border) text-(--color-muted) hover:border-(--color-text) hover:text-(--color-text)"
                      }`}
                    >
                      +
                    </button>
                    <span
                      className={`w-6 text-center text-sm font-mono tabular-nums ${
                        post.vote === 1
                          ? "text-(--color-accent)"
                          : post.vote === -1
                            ? "text-blue-400"
                            : "text-(--color-muted)"
                      }`}
                    >
                      {post.score}
                    </span>
                    <button
                      onClick={() =>
                        user && votePost(postId, post.vote === -1 ? 0 : -1)
                      }
                      disabled={!user}
                      title="Downvote"
                      className={`flex h-7 w-7 items-center justify-center rounded-full border text-sm font-bold leading-none transition-colors disabled:opacity-40 ${
                        post.vote === -1
                          ? "border-blue-400 text-blue-400"
                          : "border-(--color-border) text-(--color-muted) hover:border-(--color-text) hover:text-(--color-text)"
                      }`}
                    >
                      −
                    </button>
                  </div>

                  {/* Divider */}
                  <div className="h-6 w-px bg-(--color-border) shrink-0" />

                  {/* Time + author */}
                  <div className="flex min-w-0 flex-1 items-center gap-1.5 text-sm">
                    {timeStr && (
                      <span className="text-(--color-muted) shrink-0">
                        {timeStr}
                      </span>
                    )}
                    <span className="text-(--color-muted) shrink-0">by</span>
                    <Link
                      to={`/user/${post.user_name}`}
                      className="font-semibold text-(--color-text) hover:text-(--color-accent) truncate"
                    >
                      {post.user_name}
                    </Link>
                    <span className="inline-block h-2 w-2 shrink-0 rounded-full bg-(--color-accent) opacity-60" />
                  </div>
                </div>

                {/* Row 2: action links */}
                <div className="flex flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2 text-xs text-(--color-muted)">
                  {!isVideo && mediaSrc && (
                    <a
                      href={`https://imgops.com${mediaSrc}`}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="hover:text-(--color-text)"
                    >
                      ImgOps
                    </a>
                  )}
                  {mediaSrc && (
                    <a
                      href={mediaSrc}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="hover:text-(--color-text)"
                    >
                      original
                    </a>
                  )}
                  <button
                    onClick={handleShare}
                    className="hover:text-(--color-text)"
                  >
                    {copied ? "copied!" : "share"}
                  </button>
                  {mediaSrc && (
                    <a
                      href={mediaSrc}
                      download={post.filename}
                      className="hover:text-(--color-text)"
                    >
                      download
                    </a>
                  )}
                  <Link
                    to={`/?post=${post.id}`}
                    className="hover:text-(--color-text)"
                  >
                    permalink
                  </Link>
                </div>

                {/* Row 3: tags */}
                <div className="px-4 py-3 space-y-2">
                  <div className="flex flex-wrap items-center gap-1.5">
                    {visibleTags.map((t) => {
                      const name =
                        typeof t.name === "object" ? t.name.String : t.name;
                      return (
                        <span
                          key={t.id}
                          className="inline-flex items-center gap-1 rounded border border-(--color-border) bg-(--color-surface) px-2 py-0.5 text-xs"
                        >
                          <Link
                            to={`/?q=${encodeURIComponent(name)}`}
                            className="truncate max-w-[16ch] text-(--color-text) hover:text-(--color-accent)"
                            title={name}
                          >
                            {name}
                          </Link>
                          {user && (
                            <span className="ml-0.5 flex gap-0.5">
                              <button
                                onClick={() =>
                                  voteTag(postId, t.id, t.vote === 1 ? 0 : 1)
                                }
                                title="upvote tag"
                                className={`text-[11px] font-bold leading-none ${
                                  t.vote === 1
                                    ? "text-(--color-accent)"
                                    : "text-(--color-muted) hover:text-(--color-text)"
                                }`}
                              >
                                +
                              </button>
                              <button
                                onClick={() =>
                                  voteTag(postId, t.id, t.vote === -1 ? 0 : -1)
                                }
                                title="downvote tag"
                                className={`text-[11px] font-bold leading-none ${
                                  t.vote === -1
                                    ? "text-blue-400"
                                    : "text-(--color-muted) hover:text-(--color-text)"
                                }`}
                              >
                                −
                              </button>
                            </span>
                          )}
                        </span>
                      );
                    })}
                  </div>

                  {/* Show more / add tag controls */}
                  <div className="flex items-center gap-3 text-xs text-(--color-muted)">
                    {!showAllTags && hiddenCount > 0 && (
                      <button
                        onClick={() => setShowAllTags(true)}
                        className="hover:text-(--color-text)"
                      >
                        show {hiddenCount} more…
                      </button>
                    )}
                    {showAllTags && sortedTags.length > TOP_TAGS && (
                      <button
                        onClick={() => setShowAllTags(false)}
                        className="hover:text-(--color-text)"
                      >
                        show less
                      </button>
                    )}
                    {user && !addingTag && (
                      <button
                        onClick={() => setAddingTag(true)}
                        className="hover:text-(--color-text)"
                      >
                        + add tag
                      </button>
                    )}
                    {user && addingTag && (
                      <form
                        onSubmit={handleAddTag}
                        className="flex items-center gap-1.5"
                      >
                        <input
                          ref={tagInputRef}
                          value={tagInput}
                          onChange={(e) => setTagInput(e.target.value)}
                          placeholder="tag name"
                          className="w-28 rounded border border-(--color-border) bg-(--color-surface) px-2 py-0.5 text-xs text-(--color-text) outline-none focus:border-(--color-accent)"
                        />
                        <button
                          type="submit"
                          className="text-xs text-(--color-accent) hover:opacity-80"
                        >
                          add
                        </button>
                        <button
                          type="button"
                          onClick={() => {
                            setAddingTag(false);
                            setTagError("");
                          }}
                          className="text-xs hover:text-(--color-text)"
                        >
                          cancel
                        </button>
                        {tagError && (
                          <span className="text-red-400">{tagError}</span>
                        )}
                      </form>
                    )}
                  </div>
                </div>
              </div>

              {/* Comments */}
              <section>
                <h2 className="mb-2 text-xs font-semibold uppercase tracking-wide text-(--color-muted)">
                  comments
                  {(comments ?? []).length > 0
                    ? ` (${(comments ?? []).length})`
                    : ""}
                </h2>
                <CommentForm postId={postId} />
                {(comments ?? []).map((c) => (
                  <CommentItem key={c.id} comment={c} postId={postId} />
                ))}
              </section>
            </>
          )}
        </div>
      )}
    </div>
  );
}
