import { useState, useRef } from "react";
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

const FLAGS_OPTIONS = [
  { value: 1, label: "SFW only" },
  { value: 3, label: "SFW + NSFW" },
  { value: 7, label: "SFW + NSFW + NSFL" },
  { value: 2, label: "NSFW only" },
];

/**
 * UploadModal — three-tab modal: URL download, file upload, or pr0gramm import.
 * Props:
 *   onClose()  — called when the modal should be dismissed
 */
export default function UploadModal({ onClose }) {
  const [tab, setTab] = useState("url");

  // url / file state
  const [url, setUrl] = useState("");
  const [file, setFile] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);
  const [success, setSuccess] = useState(false);
  const fileRef = useRef(null);

  // pr0gramm import state
  const [prTags, setPrTags] = useState("");
  const [prFlags, setPrFlags] = useState(1);
  const [prMaxPages, setPrMaxPages] = useState(5);
  // prProgress holds the latest SSE event — shape varies by `phase`:
  //   { phase:'fetching',    page, max_pages, total_read, at_end }
  //   { phase:'inserted',    total, skipped_dedup }
  //   { phase:'processing',  total, processed, imported, failed }
  //   { phase:'done',        total, imported, failed }
  const [prProgress, setPrProgress] = useState(null);
  const [prError, setPrError] = useState(null);

  const user = useAuthStore((s) => s.user);
  const admin = isAdmin(user);

  const createPost = usePostStore((s) => s.createPost);
  const uploadPost = usePostStore((s) => s.uploadPost);
  const importFromPr0gramm = usePostStore((s) => s.importFromPr0gramm);

  const visibleTabs = admin ? TABS : TABS.filter((t) => t.id !== "pr0gramm");

  // ── url / file submit ──────────────────────────────────────────────────────
  async function handleSubmit(e) {
    e.preventDefault();
    setError(null);
    setLoading(true);
    try {
      if (tab === "url") {
        if (!url.trim()) throw new Error("URL is required");
        await createPost(url.trim());
      } else {
        if (!file) throw new Error("Please pick a file");
        await uploadPost(file);
      }
      setSuccess(true);
      setTimeout(onClose, 800);
    } catch (err) {
      setError(err.response?.data?.error ?? err.message);
    } finally {
      setLoading(false);
    }
  }

  // ── pr0gramm import submit ─────────────────────────────────────────────────
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

  // Close on backdrop click (blocked while an import is running)
  function handleBackdrop(e) {
    if (e.target !== e.currentTarget) return;
    if (isImporting) return;
    onClose();
  }

  const isImporting =
    prProgress !== null && prProgress.phase !== "done" && !prError;

  // Phase 1: fetching JSON pages
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

  // Phase 2: processing posts
  const isProcessingPhase =
    prProgress?.phase === "processing" || prProgress?.phase === "done";
  const processPct =
    prProgress?.total > 0
      ? Math.min(
          100,
          Math.round(
            ((prProgress.processed ?? prProgress.total ?? 0) /
              prProgress.total) *
              100,
          ),
        )
      : prProgress?.phase === "done"
        ? 100
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

          {/* ── URL / File tabs ─────────────────────────────────────────── */}
          {(tab === "url" || tab === "file") && (
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
              {success && (
                <p className="text-sm font-medium text-(--color-accent)">
                  ✓ Post created!
                </p>
              )}

              <button
                type="submit"
                disabled={loading || success}
                className="rounded-lg bg-(--color-accent) py-2 text-sm font-semibold text-white disabled:opacity-50"
              >
                {loading ? "Uploading…" : "Upload"}
              </button>
            </form>
          )}

          {/* ── Pr0gramm import tab ─────────────────────────────────────── */}
          {tab === "pr0gramm" && (
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

              {/* Progress section — shown once import starts */}
              {prProgress !== null && (
                <div className="flex flex-col gap-3 rounded-lg bg-(--color-bg) p-3">
                  {/* ── Phase 1: fetching pages ───────────────────────────── */}
                  <div className="flex flex-col gap-1">
                    <div className="flex justify-between text-xs text-(--color-muted)">
                      <span className="font-medium">
                        Phase 1 — Fetching pages
                      </span>
                      <span>
                        {prProgress.page ?? prProgress.max_pages ?? prMaxPages}{" "}
                        / {prProgress.max_pages ?? prMaxPages}
                      </span>
                    </div>
                    <ProgressBar
                      value={
                        isProcessingPhase || prProgress.phase === "done"
                          ? 100
                          : fetchPct
                      }
                      status={
                        isProcessingPhase || prProgress.phase === "done"
                          ? "success"
                          : prError
                            ? "error"
                            : "active"
                      }
                    />
                    {isFetchingPhase && (
                      <span className="text-xs text-(--color-muted)">
                        {prProgress.total_read ?? 0} items read
                      </span>
                    )}
                    {prProgress.phase === "inserted" && (
                      <span className="text-xs text-(--color-accent)">
                        {prProgress.total} new posts registered
                        {prProgress.skipped_dedup > 0
                          ? `, ${prProgress.skipped_dedup} already exist`
                          : ""}
                      </span>
                    )}
                  </div>

                  {/* ── Phase 2: processing images ────────────────────────── */}
                  {isProcessingPhase && (
                    <div className="flex flex-col gap-1">
                      <div className="flex justify-between text-xs text-(--color-muted)">
                        <span className="font-medium">
                          Phase 2 — Downloading &amp; processing
                        </span>
                        <span>
                          {prProgress.processed ?? prProgress.total ?? 0} /{" "}
                          {prProgress.total ?? 0}
                        </span>
                      </div>
                      <ProgressBar
                        value={processPct}
                        status={
                          prError
                            ? "error"
                            : prProgress.phase === "done"
                              ? "success"
                              : "active"
                        }
                      />
                      <div className="flex gap-4 text-sm">
                        <span className="text-(--color-accent)">
                          ✓ {prProgress.imported ?? 0} imported
                        </span>
                        <span className="text-(--color-muted)">
                          ✗ {prProgress.failed ?? 0} failed
                        </span>
                      </div>
                    </div>
                  )}

                  {/* Done message */}
                  {prProgress.phase === "done" && !prError && (
                    <p className="text-xs font-medium text-(--color-success)">
                      Import complete!
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
                  className="flex-1 rounded-lg bg-(--color-accent) py-2 text-sm font-semibold text-white disabled:opacity-50"
                >
                  {isImporting
                    ? prProgress?.phase === "fetching"
                      ? `Fetching page ${prProgress.page}…`
                      : prProgress?.phase === "inserted"
                        ? `Starting downloads…`
                        : `Processing… (${prProgress?.processed ?? 0}/${prProgress?.total ?? 0})`
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
                    Reset
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
