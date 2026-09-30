// @ts-check

export const POST_COUNT = 10_000;
export const INITIAL_POSTS = 320;
export const LOAD_CHUNK = 320;
export const MIN_THUMBNAIL_PX = 176;
export const MAX_COLUMNS = 10;

/** @typedef {{ id: number, kind: "image" | "video", width: number, height: number, score: number, tags: string[] }} FakePost */

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

/** @template T @param {T[]} items @param {number} columns @returns {T[][]} */
export function groupRows(items, columns) {
  if (!Number.isInteger(columns) || columns < 1) throw new RangeError("columns must be >= 1");
  const rows = [];
  for (let index = 0; index < items.length; index += columns) {
    rows.push(items.slice(index, index + columns));
  }
  return rows;
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

/** @param {number} postIndex @param {number} currentVisibleCount @param {number} [chunk] */
export function requiredVisibleCount(postIndex, currentVisibleCount, chunk = LOAD_CHUNK) {
  if (postIndex < 0) return currentVisibleCount;
  const minimum = postIndex + 1;
  if (minimum <= currentVisibleCount) return currentVisibleCount;
  return Math.ceil(minimum / chunk) * chunk;
}
