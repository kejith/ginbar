import test from "node:test";
import assert from "node:assert/strict";

import {
  MAX_MESSAGE_CHARACTERS,
  clampMessageBody,
  mergeInboxConversations,
  mergeThreadMessages,
  messageBodyLength,
  messageRouteFromPath,
  messagesForDisplay,
  pathForMessagePeer,
  requestStillCurrent,
  touchThreadOrder,
} from "./messages-state.js";

const summary = (peerId, latestId, username = `peer-${peerId}`, available = true) => ({
  peer: { id: peerId, ...(username === null ? {} : { username }), available },
  latestMessage: { id: latestId, senderId: peerId, createdAt: `2026-10-07T18:${String(latestId).padStart(2, "0")}:00Z` },
});

const message = (id, senderId = 7, recipientId = 42, body = `message-${id}`) => ({
  id,
  senderId,
  recipientId,
  body,
  createdAt: `2026-10-07T18:${String(id).padStart(2, "0")}:00Z`,
});

test("message routes use numeric peer identity and remain browser-history parseable", () => {
  assert.deepEqual(messageRouteFromPath("/messages"), { kind: "inbox" });
  assert.deepEqual(messageRouteFromPath("/messages/77"), { kind: "thread", peerId: 77 });
  assert.equal(pathForMessagePeer(77), "/messages/77");
  assert.equal(messageRouteFromPath("/messages/alice"), null);
  assert.equal(messageRouteFromPath("/messages/0"), null);
});

test("inbox merge preserves authoritative ordering, deduplicates by numeric peer, and keeps unavailable peers", () => {
  const current = [summary(7, 40, "old-name"), summary(8, 35)];
  const incoming = [summary(9, 30), summary(7, 40, "old-name"), summary(10, 25, null, false)];
  const merged = mergeInboxConversations(current, incoming);
  assert.deepEqual(merged.map((item) => item.peer.id), [7, 8, 9, 10]);
  assert.equal(merged.filter((item) => item.peer.id === 7).length, 1);
  assert.deepEqual(merged.at(-1).peer, { id: 10, available: false });
});

test("fresh inbox replacement accepts a moved conversation in its new server position", () => {
  const retained = [summary(7, 40), summary(8, 35), summary(9, 30)];
  const refreshed = mergeInboxConversations(retained, [summary(9, 55), summary(7, 40)], true);
  assert.deepEqual(refreshed.map((item) => [item.peer.id, item.latestMessage.id]), [[9, 55], [7, 40]]);
  assert.deepEqual(mergeInboxConversations([], [], true), []);
});

test("thread merge supports initial and older pages without duplicates and keeps descending internal order", () => {
  const first = mergeThreadMessages([], [message(12), message(11), message(10)], true);
  const older = mergeThreadMessages(first, [message(10), message(9), message(8)]);
  assert.deepEqual(older.map((item) => item.id), [12, 11, 10, 9, 8]);
  assert.deepEqual(messagesForDisplay(older).map((item) => item.id), [8, 9, 10, 11, 12]);
});

test("stale peer and route epochs are rejected", () => {
  assert.equal(requestStillCurrent(7, 3, 7, 3), true);
  assert.equal(requestStillCurrent(7, 3, 8, 3), false);
  assert.equal(requestStillCurrent(7, 3, 7, 4), false);
  assert.equal(requestStillCurrent(null, 5, null, 5), true);
});

test("authoritative rapid send responses merge exactly once in immutable-ID order", () => {
  let retained = [message(20)];
  retained = mergeThreadMessages(retained, [message(22, 42, 7, "second response")]);
  retained = mergeThreadMessages(retained, [message(21, 42, 7, "first response")]);
  retained = mergeThreadMessages(retained, [message(22, 42, 7, "duplicate response")]);
  assert.deepEqual(retained.map((item) => item.id), [22, 21, 20]);
  assert.equal(retained.filter((item) => item.id === 22).length, 1);
});

test("retained thread order is bounded and deterministic", () => {
  assert.deepEqual(touchThreadOrder([9, 8, 7, 6], 8, 4), [8, 9, 7, 6]);
  assert.deepEqual(touchThreadOrder([9, 8, 7, 6], 10, 4), [10, 9, 8, 7]);
});

test("message body counting and clamping use Unicode code points", () => {
  assert.equal(messageBodyLength("a😀b"), 3);
  const value = "😀".repeat(MAX_MESSAGE_CHARACTERS + 2);
  const clamped = clampMessageBody(value);
  assert.equal(messageBodyLength(clamped), MAX_MESSAGE_CHARACTERS);
});
