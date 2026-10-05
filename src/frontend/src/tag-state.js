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

/**
 * A transport failure or server error can have an ambiguous mutation outcome.
 * Deterministic client/auth/not-found failures cannot have applied the mutation.
 *
 * @param {number | undefined} status
 */
export function shouldReconcileTagMutationFailure(status) {
  return status === undefined || status >= 500;
}
