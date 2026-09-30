// @ts-check

export const POST_COUNT = 10_000;
export const INITIAL_POSTS = 320;
export const LOAD_CHUNK = 320;
export const MIN_THUMBNAIL_PX = 176;
export const MAX_COLUMNS = 10;

/** @typedef {{ id: number, kind: "image" | "video", width: number, height: number, score: number, tags: string[] }} FakePost */
/** @typedef {{ start: number, end: number }} IndexRange */

/** @returns {FakePost[]} */
export function makeFakePosts(count = POST_COUNT) {
  return Array.from({ length: count }, (_, index) => {
    const id = count - index;
    const landscape = id % 3 !== 0;
    return {
      id,
      kind: id % 7 === 0 ? "video" : "image",
      width: landscape ? 1920 : 1280,
      height: landscape ? 1080 : 1920,
      score: ((id * 37) % 801) - 120,
      tags: [`tag-${id % 17}`, `set-${id % 5}`],
    };
  });
}

/** @param {number} width */
export function columnsForWidth(width) {
  if (!Number.isFinite(width) || width <= 0) return 1;
  return Math.max(1, Math.min(MAX_COLUMNS, Math.floor(width / MIN_THUMBNAIL_PX)));
}

/** @param {number} postIndex @param {number} columns */
export function rowIndexForPostIndex(postIndex, columns) {
  if (!Number.isInteger(postIndex) || postIndex < 0) return -1;
  if (!Number.isInteger(columns) || columns < 1) return -1;
  return Math.floor(postIndex / columns);
}

/** @param {number} rowIndex @param {number} columns */
export function rowStartIndex(rowIndex, columns) {
  if (!Number.isInteger(rowIndex) || rowIndex < 0) return -1;
  if (!Number.isInteger(columns) || columns < 1) return -1;
  return rowIndex * columns;
}

/** @param {IndexRange} range @param {number} columns */
export function rowRangeForIndexRange(range, columns) {
  if (!Number.isInteger(columns) || columns < 1) throw new RangeError("columns must be >= 1");
  if (!Number.isInteger(range.start) || !Number.isInteger(range.end) || range.start < 0 || range.end < range.start) {
    throw new RangeError("invalid index range");
  }
  if (range.start === range.end) return { start: 0, end: 0 };
  return {
    start: Math.floor(range.start / columns),
    end: Math.ceil(range.end / columns),
  };
}

/** @param {number} postIndex @param {number} postCount @param {number} [windowSize] @returns {IndexRange} */
export function rangeAroundIndex(postIndex, postCount, windowSize = INITIAL_POSTS) {
  if (!Number.isInteger(postCount) || postCount < 0) throw new RangeError("postCount must be >= 0");
  if (postCount === 0) return { start: 0, end: 0 };
  if (!Number.isInteger(postIndex) || postIndex < 0 || postIndex >= postCount) throw new RangeError("postIndex out of range");
  if (!Number.isInteger(windowSize) || windowSize < 1) throw new RangeError("windowSize must be >= 1");

  const size = Math.min(windowSize, postCount);
  let start = Math.max(0, postIndex - Math.floor(size / 2));
  let end = Math.min(postCount, start + size);
  start = Math.max(0, end - size);
  return { start, end };
}

/** @param {IndexRange} range @param {number} postIndex @param {number} postCount @param {number} [chunk] @returns {IndexRange} */
export function extendRangeToIndex(range, postIndex, postCount, chunk = LOAD_CHUNK) {
  if (!Number.isInteger(postCount) || postCount < 0) throw new RangeError("postCount must be >= 0");
  if (!Number.isInteger(postIndex) || postIndex < 0 || postIndex >= postCount) throw new RangeError("postIndex out of range");
  if (!Number.isInteger(chunk) || chunk < 1) throw new RangeError("chunk must be >= 1");
  if (postIndex >= range.start && postIndex < range.end) return range;
  if (postIndex < range.start) {
    return { start: Math.max(0, Math.min(postIndex, range.start - chunk)), end: range.end };
  }
  return { start: range.start, end: Math.min(postCount, Math.max(postIndex + 1, range.end + chunk)) };
}

/** @param {IndexRange} range @param {number} postCount @param {-1 | 1} direction @param {number} [chunk] @returns {IndexRange} */
export function extendRange(range, postCount, direction, chunk = LOAD_CHUNK) {
  if (direction !== -1 && direction !== 1) throw new RangeError("direction must be -1 or 1");
  if (direction === -1) return { start: Math.max(0, range.start - chunk), end: range.end };
  return { start: range.start, end: Math.min(postCount, range.end + chunk) };
}

/** @param {string} pathname */
export function postIdFromPath(pathname) {
  const match = /^\/post\/(\d+)\/?$/.exec(pathname);
  if (!match) return null;
  const id = Number(match[1]);
  return Number.isSafeInteger(id) && id > 0 ? id : null;
}

/** @param {number} id */
export function pathForPost(id) {
  if (!Number.isSafeInteger(id) || id <= 0) throw new RangeError("post id must be a positive safe integer");
  return `/post/${id}`;
}

/** @param {number} currentIndex @param {-1 | 1} direction @param {number} postCount */
export function nextPostIndex(currentIndex, direction, postCount) {
  if (!Number.isInteger(currentIndex) || !Number.isInteger(postCount) || postCount < 1) return -1;
  if (direction !== -1 && direction !== 1) throw new RangeError("direction must be -1 or 1");
  return Math.max(0, Math.min(postCount - 1, currentIndex + direction));
}
