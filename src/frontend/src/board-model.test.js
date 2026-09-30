import assert from "node:assert/strict";
import test from "node:test";

import {
  columnsForWidth,
  groupRows,
  nextPostIndex,
  pathForPost,
  postIdFromPath,
  requiredVisibleCount,
  rowIndexForPostIndex,
  rowStartIndex,
} from "./board-model.js";

test("responsive columns stay deterministic and bounded", () => {
  assert.equal(columnsForWidth(0), 1);
  assert.equal(columnsForWidth(175), 1);
  assert.equal(columnsForWidth(352), 2);
  assert.equal(columnsForWidth(1760), 10);
  assert.equal(columnsForWidth(4000), 10);
});

test("row mapping is stable", () => {
  assert.equal(rowIndexForPostIndex(0, 4), 0);
  assert.equal(rowIndexForPostIndex(3, 4), 0);
  assert.equal(rowIndexForPostIndex(4, 4), 1);
  assert.equal(rowStartIndex(3, 4), 12);
  assert.deepEqual(groupRows([1, 2, 3, 4, 5], 2), [[1, 2], [3, 4], [5]]);
});

test("canonical post paths round-trip and reject non-post routes", () => {
  assert.equal(pathForPost(42), "/post/42");
  assert.equal(postIdFromPath("/post/42"), 42);
  assert.equal(postIdFromPath("/post/42/"), 42);
  assert.equal(postIdFromPath("/post/nope"), null);
  assert.equal(postIdFromPath("/"), null);
});

test("keyboard navigation clamps at feed ends", () => {
  assert.equal(nextPostIndex(0, -1, 100), 0);
  assert.equal(nextPostIndex(0, 1, 100), 1);
  assert.equal(nextPostIndex(99, 1, 100), 99);
});

test("deep-link selection grows incremental retention only as far as needed", () => {
  assert.equal(requiredVisibleCount(100, 320), 320);
  assert.equal(requiredVisibleCount(321, 320), 640);
  assert.equal(requiredVisibleCount(9999, 320), 10240);
});
