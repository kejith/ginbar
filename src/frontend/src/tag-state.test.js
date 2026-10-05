import assert from "node:assert/strict";
import test from "node:test";

import { canApplyTagSnapshot, shouldReconcileTagMutationFailure } from "./tag-state.js";

test("accepts the current selected-post response", () => {
  assert.equal(canApplyTagSnapshot(42, 42, 42, 3, 3), true);
});

test("rejects stale selection, epoch, mismatched response and aborted work", () => {
  assert.equal(canApplyTagSnapshot(43, 42, 42, 3, 3), false);
  assert.equal(canApplyTagSnapshot(42, 42, 42, 2, 3), false);
  assert.equal(canApplyTagSnapshot(42, 42, 43, 3, 3), false);
  assert.equal(canApplyTagSnapshot(42, 42, 42, 3, 3, true), false);
});

test("reconciles only failures with potentially ambiguous mutation outcomes", () => {
  assert.equal(shouldReconcileTagMutationFailure(undefined), true);
  assert.equal(shouldReconcileTagMutationFailure(500), true);
  assert.equal(shouldReconcileTagMutationFailure(503), true);
  assert.equal(shouldReconcileTagMutationFailure(400), false);
  assert.equal(shouldReconcileTagMutationFailure(401), false);
  assert.equal(shouldReconcileTagMutationFailure(403), false);
  assert.equal(shouldReconcileTagMutationFailure(404), false);
});
