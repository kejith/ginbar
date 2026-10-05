export const MEDIA_STATUS_POLL_MS = 2_000;

/** @typedef {"waiting" | "processing" | "retrying" | "failed" | "ready"} MediaStatusPhase */
/** @typedef {"initial" | "regeneration"} MediaStatusOperation */

/**
 * @typedef {object} MediaStatus
 * @property {MediaStatusPhase} phase
 * @property {MediaStatusOperation=} operation
 * @property {boolean} usableMedia
 * @property {{code: string, message: string}=} failure
 */

/** @param {MediaStatus | null | undefined} status */
export function mediaStatusPollDelay(status) {
  if (!status) return null;
  return status.phase === "waiting" || status.phase === "processing" || status.phase === "retrying"
    ? MEDIA_STATUS_POLL_MS
    : null;
}

/** @param {MediaStatus} status */
export function mediaStatusText(status) {
  const replacement = status.operation === "regeneration";
  switch (status.phase) {
    case "waiting":
      return replacement ? "Replacement queued" : "Waiting for processing";
    case "processing":
      return replacement ? "Processing replacement" : "Processing media";
    case "retrying":
      return replacement ? "Replacement retry scheduled" : "Processing retry scheduled";
    case "failed":
      return status.failure?.message ?? (replacement
        ? "Replacement processing failed; existing media is still available."
        : "Media processing failed.");
    case "ready":
      return "Ready";
    default:
      return "Processing status unavailable";
  }
}
