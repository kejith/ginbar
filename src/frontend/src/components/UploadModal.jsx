import { useState, useRef, useEffect } from "react";
import { createPortal } from "react-dom";
import usePostStore from "../stores/postStore.js";
import useAuthStore from "../stores/authStore.js";
import { isAdmin } from "../utils/roles.js";
import Tabs from "./Tabs.jsx";
import ProgressBar from "./ProgressBar.jsx";

const TABS = [
  { id: "url", label: "From URL" },
  { id: "file", label: "File upload" },
  { id: "pr0gramm", label: "pr0gramm" },
];

const QUEUE_TAB = { id: "queue", label: "In Queue" };

const FLAGS_OPTIONS = [
  { value: 1, label: "SFW only" },
  { value: 3, label: "SFW + NSFW" },
  { value: 7, label: "SFW + NSFW + NSFL" },
  { value: 2, label: "NSFW only" },
];

function fmtETA(sec) {
  if (sec < 0 || sec == null) return null;
  if (sec === 0) return "any moment";
  if (sec < 60) return `~${sec}s`;
  if (sec < 3600) return `~${Math.floor(sec / 60)}m ${sec % 60}s`;
  return `~${Math.floor(sec / 3600)}h ${Math.floor((sec % 3600) / 60)}m`;
}

/**
 * UploadModal — three-tab modal: URL download, file upload, or pr0gramm import.
 *
 * Uploads are now asynchronous: the backend creates a dirty post and the
 * background queue picks it up.  The modal shows live queue position + ETA
 * and auto-closes once the post is finalized.
 *
 * Members can only have one post in the queue at a time.  The pr0gramm import
 * is admin-only and can always be triggered regardless of the queue state.
 *
 * Props:
 *   onClose()      — called when the modal should be dismissed
 *   initialFile    — File object to pre-fill in the "File upload" tab
 *   initialUrl     — URL string to pre-fill in the "From URL" tab
 */
export default function UploadModal({
  onClose,
  initialFile = null,
  initialUrl = "",
}) {
  const [tab, setTab] = useState(initialFile ? "file" : "url");

  // ── URL / File state ──────────────────────────────────────────────────────
  const [url, setUrl] = useState(initialUrl);
  const [file, setFile] = useState(initialFile);
  const [error, setError] = useState(null);
  const [submitting, setSubmitting] = useState(false);
  // Once the post is queued, queueInfo is set and we poll for completion.
  const [queueInfo, setQueueInfo] = useState(null); // { post_id, queue_position, eta_sec }
  const [queueDone, setQueueDone] = useState(false);
  const [duplicates, setDuplicates] = useState(null); // [{id, thumbnail_filename, hamming_distance}]
  const fileRef = useRef(null);
  const pollRef = useRef(null);

  // ── Pr0gramm state ────────────────────────────────────────────────────────
  const [prTags, setPrTags] = useState("");
  const [prFlags, setPrFlags] = useState(1);
  const [prMaxPages, setPrMaxPages] = useState(5);
  // prProgress holds the latest SSE event — shape varies by `phase`:
  //   { phase:'fetching',  page, max_pages, total_read, at_end, success_pages, failed_pages }
  //   { phase:'inserted',  total, filtered_ext, skipped_dedup, insert_errors }
  //   { phase:'done',      total }
  const [prProgress, setPrProgress] = useState(null);
  const [prError, setPrError] = useState(null);

  const user = useAuthStore((s) => s.user);
  const admin = isAdmin(user);

  const createPost = usePostStore((s) => s.createPost);
  const uploadPost = usePostStore((s) => s.uploadPost);
  const getUserQueueStatus = usePostStore((s) => s.getUserQueueStatus);
  const getPostQueueStatus = usePostStore((s) => s.getPostQueueStatus);
  const importFromPr0gramm = usePostStore((s) => s.importFromPr0gramm);

  // ── On-mount queue check ──────────────────────────────────────────────────
  // Ask the backend whether this user already has a post in the queue.
  // If so, jump straight to the queue tab and start polling.
  const [checkingQueue, setCheckingQueue] = useState(true);
  useEffect(() => {
    let cancelled = false;
    getUserQueueStatus()
      .then((status) => {
        if (cancelled) return;
        if (status.has_post) {
          setQueueInfo({
            post_id: status.post_id,
            queue_position: status.queue_position,
            eta_sec: status.eta_sec,
          });
        }
      })
      .catch((err) => {
        // Non-fatal — user may not be logged in, or the check failed.
        // Log so it's visible in devtools without breaking the modal.
        console.warn("[UploadModal] queue check failed:", err?.message ?? err);
      })
      .finally(() => {
        if (!cancelled) setCheckingQueue(false);
      });
    return () => {
      cancelled = true;
    };
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const visibleTabs = queueInfo
    ? [QUEUE_TAB]
    : admin
      ? TABS
      : TABS.filter((t) => t.id !== "pr0gramm");

  // Auto-switch to the queue tab as soon as a post enters the queue.
  useEffect(() => {
    if (queueInfo) setTab("queue");
  }, [queueInfo?.post_id]); // eslint-disable-line react-hooks/exhaustive-deps

  // ── Queue polling effect ──────────────────────────────────────────────────
  // Polls every 2 s while this user's post is waiting in the queue.
  // Stops (and auto-closes) once dirty=false (post finalized or failed).
  useEffect(() => {
    if (!queueInfo || queueDone) return;

    const poll = async () => {
      try {
        const status = await getPostQueueStatus(queueInfo.post_id);
        if (!status.dirty) {
          clearInterval(pollRef.current);
          if (status.duplicates?.length > 0) {
            setDuplicates(status.duplicates);
            // Don't auto-close — user needs to inspect duplicates
          } else {
            setQueueDone(true);
            setTimeout(onClose, 1500);
          }
        } else {
          setQueueInfo((prev) => ({
            ...prev,
            queue_position: status.queue_position,
            eta_sec: status.eta_sec,
          }));
        }
      } catch (_) {
        // ignore transient errors; keep polling
      }
    };

    poll(); // immediate first check
    pollRef.current = setInterval(poll, 2000);
    return () => clearInterval(pollRef.current);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [queueInfo?.post_id, queueDone]);

  // ── URL / File submit ─────────────────────────────────────────────────────
  async function handleSubmit(e) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      let result;
      if (tab === "url") {
        if (!url.trim()) throw new Error("URL is required");
        result = await createPost(url.trim());
      } else {
        if (!file) throw new Error("Please pick a file");
        result = await uploadPost(file);
      }
      setQueueInfo({
        post_id: result.post_id,
        queue_position: result.queue_position,
        eta_sec: result.eta_sec,
      });
    } catch (err) {
      setError(err.response?.data?.error ?? err.message ?? "Upload failed");
    } finally {
      setSubmitting(false);
    }
  }

  // ── Pr0gramm import submit ─────────────────────────────────────────────────
  async function handlePr0grammImport(e) {
    e.preventDefault();
    if (!prTags.trim()) {
      setPrError("Tags are required");
      return;
    }
    setPrError(null);
    setPrProgress({
      phase: "fetching",
      page: 0,
      max_pages: prMaxPages,
      total_read: 0,
    });

    try {
      await importFromPr0gramm(
        { tags: prTags.trim(), flags: prFlags, maxPages: prMaxPages },
        (event) => setPrProgress(event),
      );
    } catch (err) {
      setPrError(err.message ?? "Import failed");
      // Mark as done so user can reset
      setPrProgress((p) => (p ? { ...p, phase: "done" } : p));
    }
  }

  // Close on backdrop click — blocked while a member post is in the queue
  // or a pr0gramm import is actively fetching pages.
  function handleBackdrop(e) {
    if (e.target !== e.currentTarget) return;
    if (isImporting || (queueInfo && !queueDone && !duplicates)) return;
    onClose();
  }

  const isImporting =
    prProgress !== null && prProgress.phase !== "done" && !prError;

  // Phase-1 page-fetch progress
  const isFetchingPhase =
    prProgress?.phase === "fetching" || prProgress?.phase === "inserted";
  const fetchPct = prProgress
    ? Math.min(
        100,
        Math.round(
          ((prProgress.page ?? prProgress.max_pages ?? prMaxPages) /
            (prProgress.max_pages ?? prMaxPages)) *
            100,
        ),
      )
    : 0;

  return createPortal(
    <div className="fixed inset-0 z-100 overflow-y-auto bg-black/60 backdrop-blur-sm">
      <div
        className="flex min-h-full items-center justify-center p-4"
        onClick={handleBackdrop}
      >
        <div className="w-full max-w-md rounded-xl border border-(--color-border) bg-(--color-surface) p-6 shadow-2xl">
          {/* Header */}
          <div className="mb-4 flex items-center justify-between">
            <h2 className="text-base font-semibold text-(--color-accent)">
              Upload post
            </h2>
            <button
              onClick={onClose}
              aria-label="Close"
              className="text-xl leading-none text-(--color-muted) hover:text-(--color-text) transition-colors"
            >
              ×
            </button>
          </div>

          {/* Tabs */}
          {!checkingQueue && (
            <Tabs
              tabs={visibleTabs}
              active={tab}
              onChange={(id) => {
                setTab(id);
                setError(null);
                setPrError(null);
              }}
              className="mb-5"
            />
          )}

          {/* ── Loading state while checking queue on open ──────────── */}
          {checkingQueue && (
            <div className="flex items-center justify-center py-10">
              <svg
                className="h-6 w-6 animate-spin text-(--color-accent)"
                viewBox="0 0 24 24"
                fill="none"
              >
                <circle
                  className="opacity-25"
                  cx="12"
                  cy="12"
                  r="10"
                  stroke="currentColor"
                  strokeWidth="4"
                />
                <path
                  className="opacity-75"
                  fill="currentColor"
                  d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"
                />
              </svg>
            </div>
          )}

          {/* ── URL / File tabs ─────────────────────────────────────────── */}
          {!checkingQueue && (tab === "url" || tab === "file") && (
            <form onSubmit={handleSubmit} className="flex flex-col gap-4">
              {tab === "url" ? (
                <input
                  type="url"
                  placeholder="https://example.com/image.jpg"
                  value={url}
                  onChange={(e) => setUrl(e.target.value)}
                  className="w-full rounded-lg bg-(--color-bg) px-3 py-2 text-sm text-(--color-text) outline-none ring-1 ring-(--color-border) focus:ring-(--color-accent)"
                  autoFocus
                />
              ) : (
                <div
                  className="flex cursor-pointer flex-col items-center justify-center gap-2 rounded-lg bg-(--color-bg) py-8 text-sm text-(--color-muted) ring-1 ring-(--color-border) ring-dashed hover:ring-(--color-accent)"
                  onClick={() => fileRef.current?.click()}
                >
                  <span className="text-2xl">📎</span>
                  {file ? (
                    <span className="text-(--color-text)">{file.name}</span>
                  ) : (
                    <span>Click to choose image or video</span>
                  )}
                  <input
                    ref={fileRef}
                    type="file"
                    accept="image/*,video/*"
                    className="hidden"
                    onChange={(e) => setFile(e.target.files[0] ?? null)}
                  />
                </div>
              )}

              {error && (
                <p className="rounded-lg bg-(--color-bg) px-3 py-2 text-sm text-(--color-danger)">
                  {error}
                </p>
              )}

              <button
                type="submit"
                disabled={submitting}
                className="rounded-lg bg-(--color-accent) py-2 text-sm font-semibold text-(--color-accent-text) disabled:opacity-50"
              >
                {submitting ? "Submitting…" : "Upload"}
              </button>
            </form>
          )}

          {/* ── Queue status tab ──────────────────────────────────────── */}
          {!checkingQueue && tab === "queue" && queueInfo && (
            <div className="flex flex-col gap-4">
              {duplicates ? (
                /* ── Duplicate detected ───────────────────────────────── */
                <div className="flex flex-col gap-3">
                  <div className="flex items-center gap-2 rounded-lg bg-(--color-bg) px-3 py-2 text-sm text-(--color-danger)">
                    <span className="text-base">⚠</span>
                    <span>
                      This image is a potential duplicate of{" "}
                      {duplicates.length === 1
                        ? "an existing post"
                        : `${duplicates.length} existing posts`}
                      .
                    </span>
                  </div>
                  <div
                    className={`grid gap-2 ${
                      duplicates.length === 1 ? "grid-cols-1" : "grid-cols-3"
                    }`}
                  >
                    {duplicates.map((dup) => (
                      <a
                        key={dup.id}
                        href={`/?post=${dup.id}`}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="group relative block overflow-hidden rounded-lg ring-1 ring-(--color-border) hover:ring-(--color-accent) transition-all"
                      >
                        {dup.thumbnail_filename ? (
                          <img
                            src={`/images/thumbnails/${dup.thumbnail_filename}`}
                            alt={`Post #${dup.id}`}
                            className="aspect-square w-full object-cover"
                          />
                        ) : (
                          <div className="flex aspect-square w-full items-center justify-center bg-(--color-bg) text-xs text-(--color-muted)">
                            #{dup.id}
                          </div>
                        )}
                        <div className="absolute inset-x-0 bottom-0 bg-black/60 px-1.5 py-0.5 text-center text-xs text-white opacity-0 group-hover:opacity-100 transition-opacity">
                          View #{dup.id}
                        </div>
                      </a>
                    ))}
                  </div>
                  <p className="text-center text-xs text-(--color-muted)">
                    Your upload was not saved. Click a thumbnail to inspect the
                    existing post.
                  </p>
                  <button
                    type="button"
                    onClick={onClose}
                    className="rounded-lg bg-(--color-border) py-2 text-sm font-medium text-(--color-text)"
                  >
                    Close
                  </button>
                </div>
              ) : !queueDone ? (
                <>
                  <div className="flex flex-col gap-3 rounded-lg bg-(--color-bg) p-4">
                    <div className="flex items-center gap-2 text-sm text-(--color-muted)">
                      <span className="animate-pulse text-(--color-accent)">
                        ●
                      </span>
                      <span>Processing your post…</span>
                    </div>
                    <ProgressBar value={0} status="active" />
                    <div className="flex justify-between text-sm">
                      <span className="text-(--color-muted)">
                        Queue position:{" "}
                        <span className="font-semibold text-(--color-text)">
                          {queueInfo.queue_position > 0
                            ? `#${queueInfo.queue_position}`
                            : "up next"}
                        </span>
                      </span>
                      {fmtETA(queueInfo.eta_sec) && (
                        <span className="text-(--color-muted)">
                          ETA:{" "}
                          <span className="font-semibold text-(--color-text)">
                            {fmtETA(queueInfo.eta_sec)}
                          </span>
                        </span>
                      )}
                    </div>
                  </div>
                  <p className="text-center text-xs text-(--color-muted)">
                    You can close this dialog — your post will continue
                    processing in the background.
                  </p>
                </>
              ) : (
                <div className="flex flex-col items-center gap-3 py-6">
                  <span className="text-4xl">✓</span>
                  <p className="text-sm font-semibold text-(--color-accent)">
                    Post added!
                  </p>
                  <p className="text-xs text-(--color-muted)">Closing…</p>
                </div>
              )}
            </div>
          )}

          {/* ── Pr0gramm import tab ─────────────────────────────────────── */}
          {!checkingQueue && tab === "pr0gramm" && (
            <form
              onSubmit={handlePr0grammImport}
              className="flex flex-col gap-4"
            >
              {/* Tags */}
              <div className="flex flex-col gap-1">
                <label className="text-xs font-medium text-(--color-muted)">
                  Tag search
                </label>
                <input
                  type="text"
                  placeholder="e.g. da mal Sättigung rausdrehen"
                  value={prTags}
                  onChange={(e) => setPrTags(e.target.value)}
                  disabled={isImporting}
                  className="w-full rounded-lg bg-(--color-bg) px-3 py-2 text-sm text-(--color-text) outline-none ring-1 ring-(--color-border) focus:ring-(--color-accent) disabled:opacity-50"
                  autoFocus
                />
              </div>

              {/* Flags + Max pages row */}
              <div className="flex gap-3">
                <div className="flex flex-1 flex-col gap-1">
                  <label className="text-xs font-medium text-(--color-muted)">
                    Content filter
                  </label>
                  <select
                    value={prFlags}
                    onChange={(e) => setPrFlags(Number(e.target.value))}
                    disabled={isImporting}
                    className="w-full rounded-lg bg-(--color-bg) px-3 py-2 text-sm text-(--color-text) outline-none ring-1 ring-(--color-border) focus:ring-(--color-accent) disabled:opacity-50"
                  >
                    {FLAGS_OPTIONS.map((o) => (
                      <option key={o.value} value={o.value}>
                        {o.label}
                      </option>
                    ))}
                  </select>
                </div>

                <div className="flex w-28 flex-col gap-1">
                  <label className="text-xs font-medium text-(--color-muted)">
                    Pages (max 50)
                  </label>
                  <input
                    type="number"
                    min={1}
                    max={50}
                    value={prMaxPages}
                    onChange={(e) =>
                      setPrMaxPages(
                        Math.min(50, Math.max(1, Number(e.target.value))),
                      )
                    }
                    disabled={isImporting}
                    className="w-full rounded-lg bg-(--color-bg) px-3 py-2 text-sm text-(--color-text) outline-none ring-1 ring-(--color-border) focus:ring-(--color-accent) disabled:opacity-50"
                  />
                </div>
              </div>

              {/* Fetch progress — shown once import starts */}
              {prProgress !== null && (
                <div className="flex flex-col gap-3 rounded-lg bg-(--color-bg) p-3">
                  <div className="flex flex-col gap-1">
                    <div className="flex justify-between text-xs text-(--color-muted)">
                      <span className="font-medium">Fetching pages</span>
                      <span>
                        {prProgress.page ?? prProgress.max_pages ?? prMaxPages}{" "}
                        / {prProgress.max_pages ?? prMaxPages}
                      </span>
                    </div>
                    <ProgressBar
                      value={
                        prProgress.phase === "done" ||
                        prProgress.phase === "inserted"
                          ? 100
                          : fetchPct
                      }
                      status={
                        prProgress.phase === "done" ||
                        prProgress.phase === "inserted"
                          ? "success"
                          : prError
                            ? "error"
                            : "active"
                      }
                    />
                    {isFetchingPhase && (
                      <div className="flex gap-3 text-xs">
                        <span className="text-(--color-muted)">
                          {prProgress.total_read ?? 0} items read
                        </span>
                        {(prProgress.success_pages ?? 0) > 0 && (
                          <span className="text-(--color-accent)">
                            ✓ {prProgress.success_pages} ok
                          </span>
                        )}
                        {(prProgress.failed_pages ?? 0) > 0 && (
                          <span className="text-(--color-danger)">
                            ✗ {prProgress.failed_pages} failed
                          </span>
                        )}
                      </div>
                    )}
                    {prProgress.phase === "inserted" && (
                      <div className="flex flex-col gap-1">
                        <div className="flex flex-wrap gap-x-3 gap-y-0.5 text-xs">
                          <span className="text-(--color-accent) font-medium">
                            ✓ {prProgress.total} queued
                          </span>
                          {(prProgress.skipped_dedup ?? 0) > 0 && (
                            <span className="text-(--color-muted)">
                              {prProgress.skipped_dedup} already exist
                            </span>
                          )}
                          {(prProgress.filtered_ext ?? 0) > 0 && (
                            <span className="text-(--color-muted)">
                              {prProgress.filtered_ext} unsupported format
                            </span>
                          )}
                          {(prProgress.insert_errors ?? 0) > 0 && (
                            <span className="text-(--color-danger)">
                              ✗ {prProgress.insert_errors} insert error
                              {prProgress.insert_errors !== 1 ? "s" : ""}
                            </span>
                          )}
                        </div>
                        {(prProgress.failed_pages ?? 0) > 0 && (
                          <span className="text-xs text-(--color-danger)">
                            ✗ {prProgress.failed_pages} page
                            {prProgress.failed_pages !== 1 ? "s" : ""} failed to
                            download
                          </span>
                        )}
                      </div>
                    )}
                  </div>

                  {/* Done — processing continues in the background queue */}
                  {prProgress.phase === "done" && !prError && (
                    <p className="text-xs font-medium text-(--color-success)">
                      Import queued! Processing continues in the background —
                      check the admin panel for progress.
                    </p>
                  )}
                </div>
              )}

              {/* Error */}
              {prError && (
                <p className="rounded-lg bg-(--color-bg) px-3 py-2 text-sm text-(--color-danger)">
                  {prError}
                </p>
              )}

              {/* Buttons */}
              <div className="flex gap-2">
                <button
                  type="submit"
                  disabled={isImporting || prProgress?.phase === "done"}
                  className="flex-1 rounded-lg bg-(--color-accent) py-2 text-sm font-semibold text-(--color-accent-text) disabled:opacity-50"
                >
                  {isImporting
                    ? `Fetching page ${prProgress.page}…`
                    : prProgress?.phase === "done"
                      ? "Done"
                      : "Start import"}
                </button>

                {prProgress?.phase === "done" && (
                  <button
                    type="button"
                    onClick={() => {
                      setPrProgress(null);
                      setPrError(null);
                    }}
                    className="rounded-lg bg-(--color-border) px-4 py-2 text-sm font-medium text-(--color-text)"
                  >
                    New import
                  </button>
                )}
              </div>
            </form>
          )}
        </div>
      </div>
    </div>,
    document.body,
  );
}
