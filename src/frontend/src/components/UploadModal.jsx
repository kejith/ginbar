import { useState, useRef } from "react";
import { createPortal } from "react-dom";
import usePostStore from "../stores/postStore.js";

/**
 * UploadModal — two-tab modal: URL download or file upload.
 * Props:
 *   onClose()  — called when the modal should be dismissed
 */
export default function UploadModal({ onClose }) {
  const [tab, setTab] = useState("url"); // "url" | "file"
  const [url, setUrl] = useState("");
  const [file, setFile] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);
  const [success, setSuccess] = useState(false);
  const fileRef = useRef(null);

  const createPost = usePostStore((s) => s.createPost);
  const uploadPost = usePostStore((s) => s.uploadPost);

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

  // Close on backdrop click
  function handleBackdrop(e) {
    if (e.target === e.currentTarget) onClose();
  }

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
            {["url", "file"].map((t) => (
              <button
                key={t}
                onClick={() => setTab(t)}
                className="flex-1 rounded-md py-1.5 text-sm font-medium transition-colors"
                style={{
                  background: tab === t ? "var(--color-accent)" : "transparent",
                  color: tab === t ? "#fff" : "var(--color-muted)",
                }}
              >
                {t === "url" ? "From URL" : "File upload"}
              </button>
            ))}
          </div>

          {/* Form */}
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
        </div>
      </div>
    </div>,
    document.body,
  );
}
