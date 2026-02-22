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
 * Each entry: { value, label, defaultOn }
 *
 * - guest:         empty array (always SFW, no selector shown)
 * - member:        SFW (on) | NSFP (on) | NSFW (off)
 * - secret/admin:  SFW (on) | NSFP (on) | NSFW (off) | Secret (off)
 */
export function feedFilterOptions(user) {
  if (!isMember(user)) return []; // guests get no choice — always SFW
  if (isSecret(user)) {
    return [
      { value: "nsfp", label: "NSFP", defaultOn: true },
      { value: "nsfw", label: "NSFW", defaultOn: false },
      { value: "secret", label: "Secret", defaultOn: false },
    ];
  }
  // Normal members.
  return [
    { value: "nsfp", label: "NSFP", defaultOn: true },
    { value: "nsfw", label: "NSFW", defaultOn: false },
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
