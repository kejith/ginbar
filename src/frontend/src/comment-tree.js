/**
 * @typedef {object} TreeComment
 * @property {number} id
 * @property {number | null | undefined} parentCommentId
 * @property {boolean} deleted
 */

/**
 * Merge authoritative comment rows by immutable ID and keep deterministic ID-ascending order.
 * @template {TreeComment} T
 * @param {readonly T[]} current
 * @param {readonly T[]} incoming
 * @returns {T[]}
 */
export function mergeCommentsByID(current, incoming) {
  /** @type {Map<number, T>} */
  const byID = new Map();
  for (const comment of current) byID.set(comment.id, comment);
  for (const comment of incoming) byID.set(comment.id, comment);
  return [...byID.values()].sort((left, right) => left.id - right.id);
}

/**
 * Build a preorder tree projection without recursive traversal. Input is expected to be
 * deterministic ID-ascending server state; missing parents are surfaced explicitly.
 * @template {TreeComment} T
 * @param {readonly T[]} comments
 * @returns {Array<{ comment: T, depth: number, orphaned: boolean }>}
 */
export function buildCommentRows(comments) {
  /** @type {Map<number, T>} */
  const byID = new Map();
  /** @type {Map<number, T[]>} */
  const children = new Map();
  /** @type {T[]} */
  const roots = [];
  /** @type {T[]} */
  const orphans = [];

  for (const comment of comments) byID.set(comment.id, comment);
  for (const comment of comments) {
    const parentID = comment.parentCommentId ?? null;
    if (parentID === null) {
      roots.push(comment);
      continue;
    }
    if (!byID.has(parentID)) {
      orphans.push(comment);
      continue;
    }
    const siblings = children.get(parentID);
    if (siblings) siblings.push(comment);
    else children.set(parentID, [comment]);
  }

  /** @type {Array<{ comment: T, depth: number, orphaned: boolean }>} */
  const rows = [];
  /** @type {Array<{ comment: T, depth: number }>} */
  const stack = [];
  for (let index = roots.length - 1; index >= 0; index -= 1) {
    stack.push({ comment: roots[index], depth: 0 });
  }
  while (stack.length > 0) {
    const entry = stack.pop();
    if (!entry) break;
    rows.push({ ...entry, orphaned: false });
    const descendants = children.get(entry.comment.id);
    if (!descendants) continue;
    for (let index = descendants.length - 1; index >= 0; index -= 1) {
      stack.push({ comment: descendants[index], depth: entry.depth + 1 });
    }
  }

  for (const comment of orphans) rows.push({ comment, depth: 0, orphaned: true });
  return rows;
}
