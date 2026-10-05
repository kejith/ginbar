import assert from "node:assert/strict";
import test from "node:test";

import { buildCommentRows, mergeCommentsByID } from "./comment-tree.js";

const comment = (id, parentCommentId = null, deleted = false) => ({ id, parentCommentId, deleted });

test("buildCommentRows preserves nested sibling order", () => {
  const rows = buildCommentRows([
    comment(1),
    comment(2, 1),
    comment(3, 1),
    comment(4, 2),
    comment(5),
  ]);
  assert.deepEqual(rows.map((row) => [row.comment.id, row.depth, row.orphaned]), [
    [1, 0, false],
    [2, 1, false],
    [4, 2, false],
    [3, 1, false],
    [5, 0, false],
  ]);
});

test("children attach even when input order is child before parent", () => {
  const rows = buildCommentRows([comment(2, 1), comment(1)]);
  assert.deepEqual(rows.map((row) => [row.comment.id, row.depth, row.orphaned]), [
    [1, 0, false],
    [2, 1, false],
  ]);
});

test("deleted parents remain structural nodes", () => {
  const rows = buildCommentRows([comment(1, null, true), comment(2, 1)]);
  assert.equal(rows.length, 2);
  assert.equal(rows[0].comment.deleted, true);
  assert.equal(rows[1].depth, 1);
});

test("missing parents are explicit instead of silently reparented", () => {
  const rows = buildCommentRows([comment(10, 999)]);
  assert.deepEqual(rows.map((row) => [row.comment.id, row.depth, row.orphaned]), [[10, 0, true]]);
});

test("deep nesting uses iterative traversal", () => {
  const comments = [];
  for (let id = 1; id <= 20_000; id += 1) comments.push(comment(id, id === 1 ? null : id - 1));
  const rows = buildCommentRows(comments);
  assert.equal(rows.length, 20_000);
  assert.equal(rows[19_999].comment.id, 20_000);
  assert.equal(rows[19_999].depth, 19_999);
});

test("mergeCommentsByID deduplicates authoritative inserts and preserves ID order", () => {
  const merged = mergeCommentsByID([comment(1), comment(3)], [comment(2), comment(3, 1)]);
  assert.deepEqual(merged.map((entry) => [entry.id, entry.parentCommentId]), [
    [1, null],
    [2, null],
    [3, 1],
  ]);
});
