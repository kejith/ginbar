// @ts-check

/** @param {string} pathname */
export function userIdFromPath(pathname) {
  const match = /^\/user\/(\d+)\/?$/.exec(pathname);
  if (!match) return null;
  const id = Number(match[1]);
  return Number.isSafeInteger(id) && id > 0 ? id : null;
}

/** @param {number} id */
export function pathForUser(id) {
  if (!Number.isSafeInteger(id) || id <= 0) throw new RangeError("user id must be a positive safe integer");
  return `/user/${id}`;
}
