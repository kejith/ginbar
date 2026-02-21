import { useState, useRef } from "react";
import { createPortal } from "react-dom";
import usePostStore from "../stores/postStore.js";

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
  const [prProgress, setPrProgress] = useState(null); // null | { page, totalRead, imported, skipped, done }
  const [prError, setPrError] = useState(null);

  const createPost = usePostStore((s) => s.createPost);
  const uploadPost = usePostStore((s) => s.uploadPost);
  const importFromPr0gramm = usePostStore((s) => s.importFromPr0gramm);

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
      page: 0,
      totalRead: 0,
      imported: 0,
      skipped: 0,
      done: false,
    });

    try {
      await importFromPr0gramm(
        { tags: prTags.trim(), flags: prFlags, maxPages: prMaxPages },
        (progress) => setPrProgress(progress),
      );
    } catch (err) {
      setPrError(err.message ?? "Import failed");
      setPrProgress((p) => (p ? { ...p, done: true } : p));
    }
  }

  // Close on backdrop click (blocked while an import is running)
  function handleBackdrop(e) {
    if (e.target !== e.currentTarget) return;
    if (isImporting) return;
    onClose();
  }

  const isImporting = prProgress !== null && !prProgress.done;
  const pct = prProgress ? Math.round((prProgress.page / prMaxPages) * 100) : 0;

  return createPortal(
    <div className="fixed inset-0 z-[100] overflow-y-auto bg-black/60 backdrop-blur-sm">
      <div
        className="flex min-h-full items-center justify-center p-4"
        onClick={handleBackdrop}
      >
        <div
          className="w-full max-w-md rounded-xl p-6 shadow-2xl"
          style={{
            background: "var(--color-surface)",
            border: "1px solid var(--color-border)",
          }}
        >
          {/* Header */}
          <div className="mb-4 flex items-center justify-between">
            <h2 className="text-base font-semibold text-(--color-text)">
              Upload post
            </h2>
            <button
              onClick={onClose}
              className="text-lg leading-none text-(--color-muted) hover:text-(--color-text)"
            >
              ×
            </button>
          </div>

          {/* Tabs */}
          <div
            className="mb-5 flex gap-1 rounded-lg p-1"
            style={{ background: "var(--color-bg)" }}
          >
            {TABS.map((t) => (
              <button
                key={t.id}
                onClick={() => {
                  setTab(t.id);
                  setError(null);
                  setPrError(null);
                }}
                className="flex-1 rounded-md py-1.5 text-sm font-medium transition-colors"
                style={{
                  background:
                    tab === t.id ? "var(--color-accent)" : "transparent",
                  color: tab === t.id ? "#fff" : "var(--color-muted)",
                }}
              >
                {t.label}
              </button>
            ))}
          </div>

          {/* ── URL / File tabs ─────────────────────────────────────────── */}
          {(tab === "url" || tab === "file") && (
            <form onSubmit={handleSubmit} className="flex flex-col gap-4">
              {tab === "url" ? (
                <input
                  type="url"
                  placeholder="https://example.com/image.jpg"
                  value={url}
                  onChange={(e) => setUrl(e.target.value)}
                  className="w-full rounded-lg px-3 py-2 text-sm outline-none ring-1 ring-(--color-border) focus:ring-(--color-accent)"
                  style={{
                    background: "var(--color-bg)",
                    color: "var(--color-text)",
                  }}
                  autoFocus
                />
              ) : (
                <div
                  className="flex cursor-pointer flex-col items-center justify-center gap-2 rounded-lg py-8 text-sm ring-1 ring-(--color-border) ring-dashed hover:ring-(--color-accent)"
                  style={{
                    background: "var(--color-bg)",
                    color: "var(--color-muted)",
                  }}
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
                <p
                  className="rounded-lg px-3 py-2 text-sm text-red-400"
                  style={{ background: "var(--color-bg)" }}
                >
                  {error}
                </p>
              )}
              {success && (
                <p
                  className="text-sm font-medium"
                  style={{ color: "var(--color-accent)" }}
                >
                  ✓ Post created!
                </p>
              )}

              <button
                type="submit"
                disabled={loading || success}
                className="rounded-lg py-2 text-sm font-semibold disabled:opacity-50"
                style={{ background: "var(--color-accent)", color: "#fff" }}
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
                  className="w-full rounded-lg px-3 py-2 text-sm outline-none ring-1 ring-(--color-border) focus:ring-(--color-accent) disabled:opacity-50"
                  style={{
                    background: "var(--color-bg)",
                    color: "var(--color-text)",
                  }}
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
                    className="w-full rounded-lg px-3 py-2 text-sm outline-none ring-1 ring-(--color-border) focus:ring-(--color-accent) disabled:opacity-50"
                    style={{
                      background: "var(--color-bg)",
                      color: "var(--color-text)",
                    }}
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
                    className="w-full rounded-lg px-3 py-2 text-sm outline-none ring-1 ring-(--color-border) focus:ring-(--color-accent) disabled:opacity-50"
                    style={{
                      background: "var(--color-bg)",
                      color: "var(--color-text)",
                    }}
                  />
                </div>
              </div>

              {/* Progress section — shown after first page is requested */}
              {prProgress !== null && (
                <div
                  className="flex flex-col gap-2 rounded-lg p-3"
                  style={{ background: "var(--color-bg)" }}
                >
                  {/* Bar track */}
                  <div
                    className="h-2 w-full overflow-hidden rounded-full"
                    style={{ background: "var(--color-border)" }}
                  >
                    <div
                      className="h-full rounded-full transition-all duration-500 ease-out"
                      style={{
                        width: `${prProgress.done ? 100 : pct}%`,
                        background: prError
                          ? "#f87171"
                          : prProgress.done
                            ? "#4ade80"
                            : "var(--color-accent)",
                      }}
                    />
                  </div>

                  {/* Stats row */}
                  <div className="flex justify-between text-xs text-(--color-muted)">
                    <span>
                      Page {prProgress.page} / {prMaxPages}
                    </span>
                    <span>{prProgress.totalRead} read</span>
                  </div>

                  {/* Imported / skipped counters */}
                  <div className="flex gap-4 text-sm">
                    <span style={{ color: "var(--color-accent)" }}>
                      ✓ {prProgress.imported} imported
                    </span>
                    <span className="text-(--color-muted)">
                      ↷ {prProgress.skipped} skipped
                    </span>
                  </div>

                  {/* Done message */}
                  {prProgress.done && !prError && (
                    <p
                      className="text-xs font-medium"
                      style={{ color: "#4ade80" }}
                    >
                      Import complete!
                    </p>
                  )}
                </div>
              )}

              {/* Error */}
              {prError && (
                <p
                  className="rounded-lg px-3 py-2 text-sm text-red-400"
                  style={{ background: "var(--color-bg)" }}
                >
                  {prError}
                </p>
              )}

              {/* Buttons */}
              <div className="flex gap-2">
                <button
                  type="submit"
                  disabled={isImporting || prProgress?.done}
                  className="flex-1 rounded-lg py-2 text-sm font-semibold disabled:opacity-50"
                  style={{ background: "var(--color-accent)", color: "#fff" }}
                >
                  {isImporting
                    ? `Importing… (page ${prProgress.page})`
                    : prProgress?.done
                      ? "Done"
                      : "Start import"}
                </button>

                {prProgress?.done && (
                  <button
                    type="button"
                    onClick={() => {
                      setPrProgress(null);
                      setPrError(null);
                    }}
                    className="rounded-lg px-4 py-2 text-sm font-medium"
                    style={{
                      background: "var(--color-border)",
                      color: "var(--color-text)",
                    }}
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
