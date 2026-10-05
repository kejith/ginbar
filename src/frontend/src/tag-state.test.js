import assert from "node:assert/strict";
import test from "node:test";

import { canApplyTagSnapshot } from "./tag-state.js";

test("accepts the current selected-post response", () => {
  assert.equal(canApplyTagSnapshot(42, 42, 42, 3, 3), true);
});

test("rejects stale selection, epoch, mismatched response and aborted work", () => {
  assert.equal(canApplyTagSnapshot(43, 42, 42, 3, 3), false);
  assert.equal(canApplyTagSnapshot(42, 42, 42, 2, 3), false);
  assert.equal(canApplyTagSnapshot(42, 42, 43, 3, 3), false);
  assert.equal(canApplyTagSnapshot(42, 42, 42, 3, 3, true), false);
});
