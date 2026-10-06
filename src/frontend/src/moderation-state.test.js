import test from "node:test";
import assert from "node:assert/strict";

import { buildCommentRows } from "./comment-tree.js";
import { canApplyModerationResult, withModeratedComment } from "./moderation-state.js";

test("moderation response applies only to the live selected post epoch", () => {
  assert.equal(canApplyModerationResult(42, 42, 42, 7, 7, false), true);
  assert.equal(canApplyModerationResult(43, 42, 42, 7, 7, false), false);
  assert.equal(canApplyModerationResult(42, 42, 99, 7, 7, false), false);
  assert.equal(canApplyModerationResult(42, 42, 42, 7, 8, false), false);
  assert.equal(canApplyModerationResult(42, 42, 42, 7, 7, true), false);
});

test("moderating a parent preserves its structural node and descendants", () => {
  const parent = {
    id: 10,
    postId: 42,
    authorId: 1,
    parentCommentId: null,
    body: "parent",
    score: 3,
    userVote: 1,
    createdAt: "2026-10-06T20:00:00Z",
    deleted: false,
  };
  const child = {
    id: 11,
    postId: 42,
    authorId: 2,
    parentCommentId: 10,
    body: "child",
    score: 1,
    userVote: 0,
    createdAt: "2026-10-06T20:01:00Z",
    deleted: false,
  };

  const moderated = withModeratedComment(parent);
  assert.equal(moderated.id, parent.id);
  assert.equal(moderated.parentCommentId, null);
  assert.equal(moderated.deleted, true);
  assert.equal(moderated.body, undefined);
  assert.equal(moderated.userVote, 0);

  const rows = buildCommentRows([moderated, child]);
  assert.equal(rows.length, 2);
  assert.equal(rows[0].comment.id, 10);
  assert.equal(rows[1].comment.id, 11);
  assert.equal(rows[1].depth, 1);
  assert.equal(rows[1].orphaned, false);
});
