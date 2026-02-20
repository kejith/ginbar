/**
 * buildVirtualRows
 *
 * Converts a flat posts array into an array of row descriptors for the
 * virtualizer. If a post is expanded, an 'expanded' descriptor row is
 * inserted immediately after the row that contains that post.
 *
 * Row descriptor shapes:
 *   { type: 'posts',    items: Post[], startIndex: number }
 *   { type: 'expanded', postId: number }
 *   { type: 'loading' }
 *
 * @param {Array}   posts       flat array of post objects
 * @param {number}  columns     number of grid columns
 * @param {number|null} expandedId  id of the expanded post (or null)
 * @param {boolean} isLoading   whether more posts are being loaded
 * @returns {Array} row descriptors
 */
export function buildVirtualRows(posts, columns, expandedId, isLoading) {
  const rows = [];
  let i = 0;
  let insertedExpanded = false;
  let expandedRowIndex = null; // which row index (0-based in rows array) contains the expanded post

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
      expandedRowIndex = rows.length; // will be inserted at this position
      rows.push({ type: "expanded", postId: expandedId });
      insertedExpanded = true;
    }

    i += columns;
  }

  if (isLoading) {
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
