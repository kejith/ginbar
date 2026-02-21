/**
 * Role level constants — must match the backend api/roles.go values.
 *
 *  0  = guest  (unauthenticated)
 *  1  = member (default for new users)
 *  10 = admin
 */
export const LEVEL_GUEST = 0;
export const LEVEL_MEMBER = 1;
export const LEVEL_ADMIN = 10;

/** Returns true when the given user object has admin privileges. */
export const isAdmin = (user) =>
  user != null && user !== false && user.level >= LEVEL_ADMIN;

/** Returns true when the given user object represents a logged-in member. */
export const isMember = (user) =>
  user != null && user !== false && user.level >= LEVEL_MEMBER;

/** Human-readable role label for a given level number. */
export function roleName(level) {
  if (level >= LEVEL_ADMIN) return "admin";
  if (level >= LEVEL_MEMBER) return "member";
  return "guest";
}
