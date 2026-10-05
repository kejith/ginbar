// @ts-check

export const FEED_PAGE_SIZE = 120;
export const MAX_RETAINED_POSTS = 960;
export const AROUND_MAX_RADIUS = 60;

/**
 * Keep around requests aligned to complete thumbnail rows while respecting the
 * backend's radius cap. This minimizes regrouping when newer posts are prepended.
 *
 * @param {number} columns
 */
export function aroundRadiusForColumns(columns) {
  const normalized = Number.isInteger(columns) && columns > 0 ? columns : 1;
  return Math.max(normalized, Math.floor(AROUND_MAX_RADIUS / normalized) * normalized);
}

/**
 * @param {string} storageKey
 */
export function mediaPath(storageKey) {
  const parts = storageKey.split("/");
  if (
    parts.length < 2 ||
    parts[0] !== "media" ||
    parts.some((part) => part === "" || part === "." || part === "..")
  ) {
    throw new RangeError("invalid public media storage key");
  }
  return `/${parts.map(encodeURIComponent).join("/")}`;
}

/**
 * Canonical image and video outputs both have a sibling AVIF thumbnail.
 *
 * @param {string} storageKey
 */
export function thumbnailStorageKey(storageKey) {
  if (storageKey.endsWith(".avif")) return `${storageKey.slice(0, -5)}.thumb.avif`;
  if (storageKey.endsWith(".mp4")) return `${storageKey.slice(0, -4)}.thumb.avif`;
  throw new RangeError("unsupported canonical media storage key");
}

/**
 * @template {{ id: number }} T
 * @param {readonly T[]} existing
 * @param {readonly T[]} incoming
 * @param {"older" | "newer"} direction
 * @param {number | null} selectedId
 * @param {number} columns
 * @param {number} [maxPosts]
 */
export function mergePostWindows(
  existing,
  incoming,
  direction,
  selectedId,
  columns,
  maxPosts = MAX_RETAINED_POSTS,
) {
  if (direction !== "older" && direction !== "newer") {
    throw new RangeError("direction must be older or newer");
  }
  if (!Number.isInteger(columns) || columns < 1) {
    throw new RangeError("columns must be a positive integer");
  }
  if (!Number.isInteger(maxPosts) || maxPosts < columns) {
    throw new RangeError("maxPosts must fit at least one row");
  }

  /** @type {Map<number, T>} */
  const byId = new Map();
  for (const post of existing) {
    validatePostId(post.id);
    byId.set(post.id, post);
  }
  for (const post of incoming) {
    validatePostId(post.id);
    if (!byId.has(post.id)) byId.set(post.id, post);
  }

  let posts = [...byId.values()].sort((a, b) => b.id - a.id);
  const capacity = Math.max(columns, Math.floor(maxPosts / columns) * columns);
  const overflow = posts.length - capacity;
  let trimmedNewer = 0;
  let trimmedOlder = 0;

  if (overflow > 0) {
    const trimCount = Math.min(posts.length - 1, Math.ceil(overflow / columns) * columns);
    if (direction === "older") {
      const selectedWouldBeTrimmed = selectedId !== null && posts.slice(0, trimCount).some((post) => post.id === selectedId);
      if (selectedWouldBeTrimmed) {
        posts = posts.slice(0, posts.length - trimCount);
        trimmedOlder = trimCount;
      } else {
        posts = posts.slice(trimCount);
        trimmedNewer = trimCount;
      }
    } else {
      const selectedWouldBeTrimmed = selectedId !== null && posts.slice(posts.length - trimCount).some((post) => post.id === selectedId);
      if (selectedWouldBeTrimmed) {
        posts = posts.slice(trimCount);
        trimmedNewer = trimCount;
      } else {
        posts = posts.slice(0, posts.length - trimCount);
        trimmedOlder = trimCount;
      }
    }
  }

  return { posts, trimmedNewer, trimmedOlder };
}

/** @param {number} id */
function validatePostId(id) {
  if (!Number.isSafeInteger(id) || id <= 0) throw new RangeError("post id must be a positive safe integer");
}
