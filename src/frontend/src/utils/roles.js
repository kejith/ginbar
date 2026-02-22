/**
 * Role level constants — must match the backend api/roles.go values.
 *
 *  0  = guest  (unauthenticated)
 *  1  = member (default for new users)
 *  5  = secret
 *  10 = admin
 */
export const LEVEL_GUEST = 0;
export const LEVEL_MEMBER = 1;
export const LEVEL_SECRET = 5;
export const LEVEL_ADMIN = 10;

/** Returns true when the given user object has admin privileges. */
export const isAdmin = (user) =>
  user != null && user !== false && user.level >= LEVEL_ADMIN;

/** Returns true when the given user object has secret-or-above privileges. */
export const isSecret = (user) =>
  user != null && user !== false && user.level >= LEVEL_SECRET;

/** Returns true when the given user object represents a logged-in member. */
export const isMember = (user) =>
  user != null && user !== false && user.level >= LEVEL_MEMBER;

/** Human-readable role label for a given level number. */
export function roleName(level) {
  if (level >= LEVEL_ADMIN) return "admin";
  if (level >= LEVEL_SECRET) return "secret";
  if (level >= LEVEL_MEMBER) return "member";
  return "guest";
}

/**
 * Returns the filter options the user is allowed to choose in the feed selector.
 *
 * - guest:         only SFW (no selector shown)
 * - member:        SFW | NSFW  (NSFW covers both nsfp + nsfw)
 * - secret/admin:  SFW | NSFP | NSFW | Secret
 *
 * Each entry: { value, label }
 * value="" means "show all allowed content" (used as the NSFW option for members).
 */
export function feedFilterOptions(user) {
  if (!isMember(user)) return []; // guests get no choice — always SFW
  if (isSecret(user)) {
    return [
      { value: "", label: "All" },
      { value: "sfw", label: "SFW" },
      { value: "nsfp", label: "NSFP" },
      { value: "nsfw", label: "NSFW" },
      { value: "secret", label: "Secret" },
    ];
  }
  // Normal members: coarse SFW / NSFW toggle only.
  return [
    { value: "sfw", label: "SFW" },
    { value: "", label: "NSFW" }, // empty = all member-allowed content
  ];
}

/**
 * Returns the filter options available when setting a post's filter label
 * (used in the upload modal).
 */
export function postFilterOptions(user) {
  if (!isMember(user)) return [{ value: "sfw", label: "SFW" }];
  if (isSecret(user)) {
    return [
      { value: "sfw", label: "SFW" },
      { value: "nsfp", label: "NSFP" },
      { value: "nsfw", label: "NSFW" },
      { value: "secret", label: "Secret" },
    ];
  }
  return [
    { value: "sfw", label: "SFW" },
    { value: "nsfw", label: "NSFW" },
  ];
}
