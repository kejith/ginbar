// @ts-check

/**
 * @param {import("./api").PostVote} currentVote
 * @param {-1 | 1} direction
 * @returns {import("./api").PostVote}
 */
export function nextCommentVoteForDirection(currentVote, direction) {
  validateVote(currentVote);
  if (direction !== -1 && direction !== 1) throw new RangeError("vote direction must be -1 or 1");
  return currentVote === direction ? 0 : direction;
}

/**
 * @param {import("./api").Comment} comment
 * @param {import("./api").PostVote} vote
 * @returns {import("./api").Comment}
 */
export function withOptimisticCommentVote(comment, vote) {
  validateVote(comment.userVote);
  validateVote(vote);
  if (comment.userVote === vote) return comment;
  return {
    ...comment,
    score: comment.score + vote - comment.userVote,
    userVote: vote,
  };
}

/** @param {number} vote */
function validateVote(vote) {
  if (vote !== -1 && vote !== 0 && vote !== 1) throw new RangeError("vote must be -1, 0, or 1");
}
