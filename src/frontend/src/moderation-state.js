// @ts-check

/**
 * @param {number} currentPostId
 * @param {number} requestPostId
 * @param {number} responsePostId
 * @param {number} requestEpoch
 * @param {number} currentEpoch
 * @param {boolean} aborted
 * @returns {boolean}
 */
export function canApplyModerationResult(
  currentPostId,
  requestPostId,
  responsePostId,
  requestEpoch,
  currentEpoch,
  aborted,
) {
  return !aborted
    && currentPostId === requestPostId
    && responsePostId === requestPostId
    && requestEpoch === currentEpoch;
}

/**
 * @param {import("./api").Comment} comment
 * @returns {import("./api").Comment}
 */
export function withModeratedComment(comment) {
  if (comment.deleted) return comment;
  return {
    ...comment,
    body: undefined,
    userVote: 0,
    deleted: true,
  };
}
