/**
 * @param {number} activePostId
 * @param {number} requestPostId
 * @param {number} responsePostId
 * @param {number} requestEpoch
 * @param {number} activeEpoch
 * @param {boolean} [aborted]
 */
export function canApplyTagSnapshot(
  activePostId,
  requestPostId,
  responsePostId,
  requestEpoch,
  activeEpoch,
  aborted = false,
) {
  return !aborted
    && activePostId === requestPostId
    && responsePostId === requestPostId
    && requestEpoch === activeEpoch;
}
