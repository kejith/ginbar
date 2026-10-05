// @ts-check

/**
 * @param {import("./api").PostVote} currentVote
 * @param {-1 | 1} direction
 * @returns {import("./api").PostVote}
 */
export function nextVoteForDirection(currentVote, direction) {
  validateVote(currentVote);
  if (direction !== -1 && direction !== 1) throw new RangeError("vote direction must be -1 or 1");
  return currentVote === direction ? 0 : direction;
}

/**
 * @param {import("./api").PostSummary} post
 * @param {import("./api").PostVote} vote
 * @returns {import("./api").PostSummary}
 */
export function withOptimisticVote(post, vote) {
  validateVote(post.userVote);
  validateVote(vote);
  if (post.userVote === vote) return post;
  return {
    ...post,
    score: post.score + vote - post.userVote,
    userVote: vote,
  };
}

/** @param {number} vote */
function validateVote(vote) {
  if (vote !== -1 && vote !== 0 && vote !== 1) throw new RangeError("vote must be -1, 0, or 1");
}
