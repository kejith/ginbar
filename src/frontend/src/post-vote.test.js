import assert from "node:assert/strict";
import test from "node:test";

import { nextVoteForDirection, withOptimisticVote } from "./post-vote.js";

test("nextVoteForDirection toggles the active direction and switches opposites", () => {
  assert.equal(nextVoteForDirection(0, 1), 1);
  assert.equal(nextVoteForDirection(0, -1), -1);
  assert.equal(nextVoteForDirection(1, 1), 0);
  assert.equal(nextVoteForDirection(-1, -1), 0);
  assert.equal(nextVoteForDirection(1, -1), -1);
  assert.equal(nextVoteForDirection(-1, 1), 1);
});

test("withOptimisticVote applies exact score deltas without mutating the source", () => {
  const source = { id: 7, score: 10, userVote: 1 };
  assert.deepEqual(withOptimisticVote(source, -1), { id: 7, score: 8, userVote: -1 });
  assert.deepEqual(source, { id: 7, score: 10, userVote: 1 });
  assert.strictEqual(withOptimisticVote(source, 1), source);
  assert.deepEqual(withOptimisticVote({ score: 4, userVote: -1 }, 0), { score: 5, userVote: 0 });
});

test("vote helpers reject invalid values", () => {
  assert.throws(() => nextVoteForDirection(0, 0), RangeError);
  assert.throws(() => withOptimisticVote({ score: 0, userVote: 2 }, 1), RangeError);
  assert.throws(() => withOptimisticVote({ score: 0, userVote: 0 }, 2), RangeError);
});
