/**
 * buildVirtualRows
 *
 * Converts a flat posts array into an array of row descriptors for the
 * virtualizer. If a post is expanded, an 'expanded' descriptor row is
 * inserted immediately after the row that contains that post.
 *
 * Row descriptor shapes:
 *   { type: 'loading-top' }               — spinner prepended when loading newer
 *   { type: 'posts',    items: Post[], startIndex: number }
 *   { type: 'expanded', postId: number }
 *   { type: 'loading' }                   — spinner appended when loading older
 *
 * @param {Array}   posts          flat array of post objects
 * @param {number}  columns        number of grid columns
 * @param {number|null} expandedId id of the expanded post (or null)
 * @param {boolean} isLoadingBottom whether more older posts are being loaded
 * @param {boolean} isLoadingTop    whether more newer posts are being loaded
 * @returns {Array} row descriptors
 */
export function buildVirtualRows(
  posts,
  columns,
  expandedId,
  isLoadingBottom,
  isLoadingTop = false,
) {
  const rows = [];

  if (isLoadingTop) {
    rows.push({ type: "loading-top" });
  }

  let i = 0;
  let insertedExpanded = false;

  while (i < posts.length) {
    const slice = posts.slice(i, i + columns);
    const row = { type: "posts", items: slice, startIndex: i };
    rows.push(row);

    // Check if this row contains the expanded post
    if (
      expandedId != null &&
      !insertedExpanded &&
      slice.some((p) => p.id === expandedId)
    ) {
      rows.push({ type: "expanded", postId: expandedId });
      insertedExpanded = true;
    }

    i += columns;
  }

  if (isLoadingBottom) {
    rows.push({ type: "loading" });
  }

  return rows;
}

/**
 * Given a post id, return the index in the virtual rows array.
 * Returns -1 if not found.
 */
export function findRowIndexForPost(rows, postId) {
  for (let i = 0; i < rows.length; i++) {
    const row = rows[i];
    if (row.type === "posts" && row.items.some((p) => p.id === postId)) {
      return i;
    }
  }
  return -1;
}
