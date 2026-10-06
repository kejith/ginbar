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

export function withModeratedComment(comment) {
  if (comment.deleted) return comment;
  return {
    ...comment,
    body: undefined,
    userVote: 0,
    deleted: true,
  };
}
