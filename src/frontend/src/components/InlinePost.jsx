import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import CommentItem from "./CommentItem.jsx";
import CommentForm from "./CommentForm.jsx";
import useAuthStore from "../stores/authStore.js";
import usePostStore from "../stores/postStore.js";
import useCommentStore from "../stores/commentStore.js";
import useTagStore from "../stores/tagStore.js";
import { timeAgo } from "../utils/timeAgo.js";
import { isAdmin } from "../utils/roles.js";
import api from "../utils/api.js";

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
  const [deletingPost, setDeletingPost] = useState(false);
  const [deletingComment, setDeletingComment] = useState(null);
  const [deletingTag, setDeletingTag] = useState(null);

  const user = useAuthStore((s) => s.user);
  const admin = isAdmin(user);
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

  // Full-resolution URL derivable from listPost alone — available the moment
  // the panel mounts, before the single-post API fetch completes. Null for
  // video posts (video element still waits for isReady / mediaSrc).
  const listMediaSrc =
    !listIsVideo && listPost?.filename ? `/images/${listPost.filename}` : null;

  const sortedTags = [...(tags ?? [])].sort((a, b) => b.score - a.score);
  const visibleTags = showAllTags ? sortedTags : sortedTags.slice(0, TOP_TAGS);
  const hiddenCount = sortedTags.length - TOP_TAGS;

  const uploadedAt = post?.created_at?.Time ?? post?.created_at;
  const timeStr = timeAgo(uploadedAt);

  async function handleDeletePost() {
    if (!confirm(`Delete post #${postId}? This cannot be undone.`)) return;
    setDeletingPost(true);
    try {
      await api.delete(`/admin/posts/${postId}`);
      onClose();
    } catch (e) {
      alert(e.message);
      setDeletingPost(false);
    }
  }

  async function handleDeleteComment(commentId) {
    setDeletingComment(commentId);
    try {
      await api.delete(`/admin/comments/${commentId}`);
      seedComments(
        postId,
        (comments ?? []).filter((c) => c.id !== commentId),
      );
    } catch (e) {
      alert(e.message);
    } finally {
      setDeletingComment(null);
    }
  }

  async function handleDeleteTag(tagId) {
    setDeletingTag(tagId);
    try {
      await api.delete(`/admin/tags/${tagId}`);
      seedTags(
        postId,
        (tags ?? []).filter((t) => t.id !== tagId),
      );
    } catch (e) {
      alert(e.message);
    } finally {
      setDeletingTag(null);
    }
  }

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
    const url = `${window.location.origin}/post/${postId}`;
    navigator.clipboard.writeText(url).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  }

  // Dimension info from the list post (populated for newly uploaded content;
  // zero for legacy rows where the columns didn't exist yet).
  const hasKnownDimensions = listPost?.width > 0 && listPost?.height > 0;

  // When dimensions are known the container gets a CSS aspect-ratio so the
  // browser reserves the exact height on first paint and all children are
  // absolutely positioned inside it — zero layout shift.
  // For legacy posts (no dimensions) children flow naturally; the image
  // determines its own height so content below may shift once, but only
  // for the subset of posts that pre-date this feature.
  const mediaContainerStyle = hasKnownDimensions
    ? { aspectRatio: `${listPost.width}/${listPost.height}` }
    : {};
  const mediaChildCls = hasKnownDimensions
    ? "absolute inset-0 w-full h-full object-contain"
    : "w-full object-contain";

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
        <div className="mx-auto max-w-[920px] pb-4 space-y-3">
          {/* ── Media ── Always rendered so the thumbnail can show immediately
               and the full image can be preloaded before the fetch completes.
               When dimensions are known the aspect-ratio style reserves the
               exact height on first paint (zero CLS). When unknown the
               container grows with the content. */}
          <div
            className={`rounded-lg bg-black ${
              hasKnownDimensions ? "overflow-hidden relative" : ""
            }`}
            style={mediaContainerStyle}
          >
            {/* Blurred thumbnail placeholder.
                 - hasKnownDimensions: absolutely positioned, cross-fades out
                   (opacity-50 → opacity-0 with transition) as the full-res
                   image fades in. Both layers are inset-0 inside the
                   aspect-ratio container — zero layout shift.
                 - Legacy posts: in-flow, hidden via display:none once the
                   full-res image finishes loading. */}
            {!listIsVideo && thumbUrl && (
              <img
                src={thumbUrl}
                alt=""
                aria-hidden
                className={`blur-sm transition-opacity duration-300 ${
                  hasKnownDimensions
                    ? `absolute inset-0 w-full h-full object-contain ${
                        imgReady
                          ? "opacity-0 pointer-events-none"
                          : "opacity-50"
                      }`
                    : `w-full object-contain ${
                        imgReady ? "hidden" : "opacity-50"
                      }`
                }`}
              />
            )}

            {/* Fallback spinner when there is no thumbnail and no preload URL */}
            {postLoading && !thumbUrl && !listMediaSrc && (
              <div
                className={`flex items-center justify-center text-sm text-(--color-muted) animate-pulse ${
                  hasKnownDimensions ? "absolute inset-0" : "h-48"
                }`}
              >
                loading…
              </div>
            )}

            {/* Full-resolution image (hasKnownDimensions path).
                 Rendered immediately from listPost — no API round-trip needed.
                 Starts at opacity-0, fades in once onLoad fires while the
                 thumbnail simultaneously fades out. */}
            {hasKnownDimensions && !listIsVideo && listMediaSrc && (
              <img
                key={postId}
                src={listMediaSrc}
                alt=""
                onLoad={() => setImgReady(true)}
                className={`transition-opacity duration-300 ${mediaChildCls} ${
                  imgReady ? "opacity-100" : "opacity-0"
                }`}
              />
            )}

            {/* Full-resolution image (legacy / no-dimensions path).
                 Still waits for isReady so we don't disturb the in-flow
                 layout that determines the container height. */}
            {!hasKnownDimensions && isReady && !isVideo && mediaSrc && (
              <img
                key={mediaSrc}
                src={mediaSrc}
                alt=""
                onLoad={() => setImgReady(true)}
                className={`transition-opacity duration-300 ${mediaChildCls} ${
                  imgReady ? "opacity-100" : "opacity-0"
                }`}
              />
            )}

            {/* Video — always waits for isReady (we have no usable preload src) */}
            {isReady && isVideo && mediaSrc && (
              <video
                key={mediaSrc}
                src={mediaSrc}
                controls
                className={mediaChildCls}
              />
            )}

            {/* No media at all */}
            {isReady && !mediaSrc && !listMediaSrc && (
              <div
                className={`flex items-center justify-center text-sm text-(--color-muted) ${
                  hasKnownDimensions ? "absolute inset-0" : "h-48"
                }`}
              >
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
                    to={`/post/${post.id}`}
                    className="hover:text-(--color-text)"
                  >
                    permalink
                  </Link>
                  {admin && (
                    <button
                      disabled={deletingPost}
                      onClick={handleDeletePost}
                      className="ml-auto rounded bg-red-700 px-2 py-0.5 text-xs text-white disabled:opacity-50"
                    >
                      {deletingPost ? "deleting…" : "delete post"}
                    </button>
                  )}
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
                          {admin && (
                            <button
                              disabled={deletingTag === t.id}
                              onClick={() => handleDeleteTag(t.id)}
                              title="delete tag"
                              className="text-[11px] font-bold leading-none text-red-500 hover:text-red-400 disabled:opacity-40"
                            >
                              ×
                            </button>
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
                  <CommentItem
                    key={c.id}
                    comment={c}
                    postId={postId}
                    onDelete={
                      admin ? () => handleDeleteComment(c.id) : undefined
                    }
                    deleting={deletingComment === c.id}
                  />
                ))}
              </section>
            </>
          )}
        </div>
      )}
    </div>
  );
}
