import assert from "node:assert/strict";
import test from "node:test";

import { pathForUser, userIdFromPath } from "./profile-route.js";

test("user profile routes use immutable positive numeric ids", () => {
  assert.equal(userIdFromPath("/user/42"), 42);
  assert.equal(userIdFromPath("/user/42/"), 42);
  assert.equal(userIdFromPath("/user/0"), null);
  assert.equal(userIdFromPath("/user/-1"), null);
  assert.equal(userIdFromPath("/user/alice"), null);
  assert.equal(userIdFromPath("/post/42"), null);
  assert.equal(userIdFromPath(`/user/${Number.MAX_SAFE_INTEGER + 1}`), null);
});

test("pathForUser rejects unstable or invalid ids", () => {
  assert.equal(pathForUser(42), "/user/42");
  assert.throws(() => pathForUser(0), RangeError);
  assert.throws(() => pathForUser(1.5), RangeError);
  assert.throws(() => pathForUser(Number.MAX_SAFE_INTEGER + 1), RangeError);
});
