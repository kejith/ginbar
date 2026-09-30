import assert from "node:assert/strict";
import test from "node:test";

import {
  columnsForWidth,
  extendRange,
  extendRangeToIndex,
  makeFakePosts,
  nextPostIndex,
  pathForPost,
  postIdFromPath,
  rangeAroundIndex,
  rowIndexForPostIndex,
  rowRangeForIndexRange,
} from "./board-model.js";

test("fake posts are deterministic and descending", () => {
  const posts = makeFakePosts(4);
  assert.deepEqual(posts.map((post) => post.id), [4, 3, 2, 1]);
  assert.equal(posts[0].score, 28);
});

test("responsive columns are bounded", () => {
  assert.equal(columnsForWidth(0), 1);
  assert.equal(columnsForWidth(390), 2);
  assert.equal(columnsForWidth(1440), 8);
  assert.equal(columnsForWidth(4000), 10);
});

test("post indexes map deterministically to rows", () => {
  assert.equal(rowIndexForPostIndex(0, 8), 0);
  assert.equal(rowIndexForPostIndex(7, 8), 0);
  assert.equal(rowIndexForPostIndex(8, 8), 1);
  assert.equal(rowIndexForPostIndex(319, 8), 39);
});

test("index ranges map to absolute row ranges", () => {
  assert.deepEqual(rowRangeForIndexRange({ start: 0, end: 320 }, 8), { start: 0, end: 40 });
  assert.deepEqual(rowRangeForIndexRange({ start: 161, end: 481 }, 8), { start: 20, end: 61 });
  assert.deepEqual(rowRangeForIndexRange({ start: 12, end: 12 }, 8), { start: 0, end: 0 });
});

test("deep-link windows are bounded around the selected post", () => {
  assert.deepEqual(rangeAroundIndex(0, 10_000, 320), { start: 0, end: 320 });
  assert.deepEqual(rangeAroundIndex(5_000, 10_000, 320), { start: 4_840, end: 5_160 });
  assert.deepEqual(rangeAroundIndex(9_999, 10_000, 320), { start: 9_680, end: 10_000 });
});

test("loaded ranges extend without rebuilding the opposite edge", () => {
  const base = { start: 4_840, end: 5_160 };
  assert.deepEqual(extendRange(base, 10_000, -1), { start: 4_520, end: 5_160 });
  assert.deepEqual(extendRange(base, 10_000, 1), { start: 4_840, end: 5_480 });
  assert.deepEqual(extendRangeToIndex(base, 4_700, 10_000), { start: 4_520, end: 5_160 });
  assert.deepEqual(extendRangeToIndex(base, 5_400, 10_000), { start: 4_840, end: 5_480 });
  assert.equal(extendRangeToIndex(base, 5_000, 10_000), base);
});

test("post paths parse strictly", () => {
  assert.equal(postIdFromPath("/post/123"), 123);
  assert.equal(postIdFromPath("/post/123/"), 123);
  assert.equal(postIdFromPath("/post/nope"), null);
  assert.equal(postIdFromPath("/foo/post/123"), null);
  assert.equal(pathForPost(123), "/post/123");
  assert.throws(() => pathForPost(0), RangeError);
});

test("next post navigation clamps to feed boundaries", () => {
  assert.equal(nextPostIndex(5, 1, 10), 6);
  assert.equal(nextPostIndex(5, -1, 10), 4);
  assert.equal(nextPostIndex(0, -1, 10), 0);
  assert.equal(nextPostIndex(9, 1, 10), 9);
});
