import test from "node:test";
import assert from "node:assert/strict";

import { MEDIA_STATUS_POLL_MS, mediaStatusPollDelay, mediaStatusText } from "./media-status.js";

test("media status polling runs only while work is unresolved", () => {
  for (const phase of ["waiting", "processing", "retrying"]) {
    assert.equal(mediaStatusPollDelay({ phase, usableMedia: false }), MEDIA_STATUS_POLL_MS);
  }
  assert.equal(mediaStatusPollDelay({ phase: "failed", usableMedia: false }), null);
  assert.equal(mediaStatusPollDelay({ phase: "ready", usableMedia: true }), null);
  assert.equal(mediaStatusPollDelay(null), null);
});

test("regeneration copy never implies the existing media disappeared", () => {
  assert.equal(
    mediaStatusText({
      phase: "failed",
      operation: "regeneration",
      usableMedia: true,
      failure: {
        code: "processing_failed",
        message: "Replacement processing failed; existing media is still available.",
      },
    }),
    "Replacement processing failed; existing media is still available.",
  );
  assert.equal(
    mediaStatusText({ phase: "processing", operation: "regeneration", usableMedia: true }),
    "Processing replacement",
  );
});
