import assert from "node:assert/strict";
import test from "node:test";

import {
  AROUND_MAX_RADIUS,
  MAX_RETAINED_POSTS,
  aroundRadiusForColumns,
  mediaPath,
  mergePostWindows,
  thumbnailStorageKey,
} from "./board-data.js";

const posts = (high, low) => Array.from({ length: high - low + 1 }, (_, index) => ({ id: high - index }));

test("around radius is row aligned and backend bounded", () => {
  for (let columns = 1; columns <= 10; columns += 1) {
    const radius = aroundRadiusForColumns(columns);
    assert.ok(radius <= AROUND_MAX_RADIUS);
    assert.equal(radius % columns, 0);
  }
  assert.equal(aroundRadiusForColumns(8), 56);
  assert.equal(aroundRadiusForColumns(10), 60);
});

test("media paths expose only the canonical media tree", () => {
  const image = "media/01/513/v1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.avif";
  const video = "media/02/514/v1-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.mp4";
  assert.equal(mediaPath(image), `/${image}`);
  assert.equal(
    thumbnailStorageKey(image),
    "media/01/513/v1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.thumb.avif",
  );
  assert.equal(
    thumbnailStorageKey(video),
    "media/02/514/v1-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.thumb.avif",
  );
  assert.throws(() => mediaPath("sources/01/file"), RangeError);
  assert.throws(() => mediaPath("media/../source"), RangeError);
  assert.throws(() => thumbnailStorageKey("media/01/file.webm"), RangeError);
});

test("older pages remain descending and trim whole newer rows", () => {
  const existing = posts(1100, 141);
  const incoming = posts(140, 21);
  const result = mergePostWindows(existing, incoming, "older", null, 8);
  assert.equal(result.posts.length, MAX_RETAINED_POSTS);
  assert.equal(result.trimmedNewer, 120);
  assert.equal(result.trimmedOlder, 0);
  assert.equal(result.posts[0].id, 980);
  assert.equal(result.posts.at(-1).id, 21);
  assert.ok(result.posts.every((post, index, all) => index === 0 || all[index - 1].id > post.id));
});

test("newer pages trim whole older rows", () => {
  const existing = posts(980, 21);
  const incoming = posts(1100, 981);
  const result = mergePostWindows(existing, incoming, "newer", null, 8);
  assert.equal(result.posts.length, MAX_RETAINED_POSTS);
  assert.equal(result.trimmedNewer, 0);
  assert.equal(result.trimmedOlder, 120);
  assert.equal(result.posts[0].id, 1100);
  assert.equal(result.posts.at(-1).id, 141);
});

test("selected posts are retained when the normal trim edge would evict them", () => {
  const existing = posts(1100, 141);
  const older = posts(140, 21);
  const olderResult = mergePostWindows(existing, older, "older", 1099, 8);
  assert.ok(olderResult.posts.some((post) => post.id === 1099));
  assert.equal(olderResult.trimmedOlder, 120);

  const newer = posts(1220, 1101);
  const newerResult = mergePostWindows(existing, newer, "newer", 142, 8);
  assert.ok(newerResult.posts.some((post) => post.id === 142));
  assert.equal(newerResult.trimmedNewer, 120);
});

test("duplicate overlap preserves existing object identity", () => {
  const existing = posts(10, 5);
  const incoming = [{ id: 5 }, { id: 4 }, { id: 3 }];
  const retainedFive = existing.at(-1);
  const result = mergePostWindows(existing, incoming, "older", null, 2, 20);
  assert.equal(result.posts.find((post) => post.id === 5), retainedFive);
  assert.deepEqual(result.posts.map((post) => post.id), [10, 9, 8, 7, 6, 5, 4, 3]);
});
