import assert from "node:assert/strict";
import test from "node:test";

import { nextCommentVoteForDirection, withOptimisticCommentVote } from "./comment-vote.js";

test("nextCommentVoteForDirection covers all explicit transitions", () => {
  assert.equal(nextCommentVoteForDirection(0, 1), 1);
  assert.equal(nextCommentVoteForDirection(0, -1), -1);
  assert.equal(nextCommentVoteForDirection(1, 1), 0);
  assert.equal(nextCommentVoteForDirection(-1, -1), 0);
  assert.equal(nextCommentVoteForDirection(1, -1), -1);
  assert.equal(nextCommentVoteForDirection(-1, 1), 1);
});

test("withOptimisticCommentVote applies exact score deltas without mutating siblings", () => {
  const source = { id: 7, score: 10, userVote: 1 };
  assert.deepEqual(withOptimisticCommentVote(source, -1), { id: 7, score: 8, userVote: -1 });
  assert.deepEqual(source, { id: 7, score: 10, userVote: 1 });
  assert.strictEqual(withOptimisticCommentVote(source, 1), source);
  assert.deepEqual(withOptimisticCommentVote({ score: 4, userVote: -1 }, 0), { score: 5, userVote: 0 });
});

test("comment vote helpers reject invalid values", () => {
  assert.throws(() => nextCommentVoteForDirection(0, 0), RangeError);
  assert.throws(() => withOptimisticCommentVote({ score: 0, userVote: 2 }, 1), RangeError);
  assert.throws(() => withOptimisticCommentVote({ score: 0, userVote: 0 }, 2), RangeError);
});
