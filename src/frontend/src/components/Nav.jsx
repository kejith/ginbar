import { useRef, useState, useEffect } from "react";
import {
  Link,
  useNavigate,
  useSearchParams,
  useLocation,
} from "react-router-dom";
import useAuthStore from "../stores/authStore.js";
import useMessageStore from "../stores/messageStore.js";
import usePostStore from "../stores/postStore.js";
import UploadModal from "./UploadModal.jsx";
import SettingsMenu from "./SettingsMenu.jsx";
import { isAdmin } from "../utils/roles.js";

export default function Nav() {
  const user = useAuthStore((s) => s.user);
  const logout = useAuthStore((s) => s.logout);
  const unread = useMessageStore((s) => s.unread);
  const fetchUnread = useMessageStore((s) => s.fetchUnread);
  const resetHome = usePostStore((s) => s.resetHome);
  const inputRef = useRef(null);
  const navigate = useNavigate();
  const [showUpload, setShowUpload] = useState(false);
  const [preloadFile, setPreloadFile] = useState(null);
  const [preloadUrl, setPreloadUrl] = useState("");
  const [dragging, setDragging] = useState(false);
  const [searchParams] = useSearchParams();
  const location = useLocation();

  // Poll unread count every 30 s while logged in.
  useEffect(() => {
    if (!user) return;
    fetchUnread();
    const id = setInterval(fetchUnread, 30_000);
    return () => clearInterval(id);
  }, [user, fetchUnread]);

  // Global drag-and-drop upload
  useEffect(() => {
    if (!user) return;

    function onDragOver(e) {
      if ([...e.dataTransfer.items].some((i) => i.kind === "file")) {
        e.preventDefault();
        setDragging(true);
      }
    }
    function onDragLeave(e) {
      // only clear when leaving the viewport entirely
      if (e.relatedTarget === null) setDragging(false);
    }
    function onDrop(e) {
      e.preventDefault();
      setDragging(false);
      const f = e.dataTransfer.files[0];
      if (!f) return;
      if (!f.type.startsWith("image/") && !f.type.startsWith("video/")) return;
      setPreloadFile(f);
      setShowUpload(true);
    }

    document.addEventListener("dragover", onDragOver);
    document.addEventListener("dragleave", onDragLeave);
    document.addEventListener("drop", onDrop);
    return () => {
      document.removeEventListener("dragover", onDragOver);
      document.removeEventListener("dragleave", onDragLeave);
      document.removeEventListener("drop", onDrop);
    };
  }, [user]);

  // Global Ctrl+V clipboard image upload
  useEffect(() => {
    if (!user) return;

    // URL pointing to an image or video file
    const MEDIA_URL_RE =
      /^https?:\/\/.+\.(?:jpe?g|png|gif|webp|avif|bmp|tiff?|svg|mp4|webm|mov|avi|mkv)(\?.*)?$/i;

    function onPaste(e) {
      // ignore when typing in an input/textarea
      const tag = document.activeElement?.tagName;
      if (tag === "INPUT" || tag === "TEXTAREA") return;

      const items = [...(e.clipboardData?.items ?? [])];

      // 1. Raw image data (screenshot, copy-image, etc.)
      const imageItem = items.find((i) => i.type.startsWith("image/"));
      if (imageItem) {
        const f = imageItem.getAsFile();
        if (f) {
          e.preventDefault();
          setPreloadFile(f);
          setPreloadUrl("");
          setShowUpload(true);
          return;
        }
      }

      // 2. Plain-text URL that points to an image or video
      const text = e.clipboardData?.getData("text")?.trim() ?? "";
      if (MEDIA_URL_RE.test(text)) {
        e.preventDefault();
        setPreloadUrl(text);
        setPreloadFile(null);
        setShowUpload(true);
      }
    }

    document.addEventListener("paste", onPaste);
    return () => document.removeEventListener("paste", onPaste);
  }, [user]);

  // Keep the search input in sync with the current URL.
  useEffect(() => {
    // Check if we're on a /user/:name[/:tags[/:postId]] route.
    // /user/:name/posts[/:tags[/:postId]]
    const userPostsMatch = location.pathname.match(
      /^\/user\/([^/]+)\/posts(?:\/([^/]+?))?(?:\/\d+)?\/?$/,
    );
    const profileOnlyMatch =
      !userPostsMatch && location.pathname.match(/^\/user\/([^/]+)\/?$/);

    if (userPostsMatch) {
      const uName = userPostsMatch[1];
      const seg = userPostsMatch[2];
      const tags = seg && !/^\d+$/.test(seg) ? seg : "";
      if (inputRef.current)
        inputRef.current.value = tags
          ? `user:${uName} ${tags}`
          : `user:${uName}`;
    } else if (profileOnlyMatch) {
      const uName = profileOnlyMatch[1];
      if (inputRef.current) inputRef.current.value = `user:${uName}`;
    } else {
      const q = searchParams.get("q") || "";
      if (inputRef.current) inputRef.current.value = q;
    }
  }, [location.pathname, searchParams]);

  function handleSearch(e) {
    e.preventDefault();
    const raw = inputRef.current?.value.trim();
    if (!raw) {
      navigate("/");
      return;
    }
    // Extract "user:name" token from anywhere in the input.
    // Everything else is treated as tag keywords.
    const userMatch = raw.match(/(?:^|\s)user:(\S+)/i);
    const uName = userMatch ? userMatch[1] : null;
    const tags = raw.replace(/(?:^|\s)user:\S+/gi, " ").trim();

    if (uName) {
      // Produce clean path: /user/:name/posts[/:tags]
      navigate(
        tags
          ? `/user/${uName}/posts/${encodeURIComponent(tags)}`
          : `/user/${uName}/posts`,
      );
    } else {
      navigate(`/?q=${encodeURIComponent(tags)}`);
    }
  }

  return (
    <nav className="sticky top-0 z-50 flex h-12 items-center gap-3 border-b border-(--color-border) bg-(--color-surface)/90 px-3 backdrop-blur-sm">
      {/* Logo */}
      <button
        onClick={() => {
          if (inputRef.current) inputRef.current.value = "";
          navigate("/");
          resetHome();
        }}
        className="shrink-0 text-lg text-(--color-accent) cursor-pointer"
        style={{
          fontWeight: "var(--brand-weight)",
          letterSpacing: "var(--brand-tracking)",
          background: "none",
          border: "none",
          padding: 0,
        }}
      >
        Wallium
      </button>

      {/* Search */}
      <form onSubmit={handleSearch} className="flex min-w-0 flex-1 gap-2">
        <input
          ref={inputRef}
          type="search"
          placeholder="tags… or user:name…"
          className="h-8 w-full min-w-0 rounded-sm bg-(--color-bg) px-3 text-sm text-(--color-text) placeholder:text-(--color-muted) outline-none ring-1 ring-(--color-border) focus:ring-(--color-accent)"
        />
        <button
          type="submit"
          title="Search"
          className="shrink-0 flex items-center justify-center h-8 w-8 rounded-sm bg-(--color-accent) text-(--color-accent-text)"
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2.2"
            strokeLinecap="round"
            strokeLinejoin="round"
            className="h-4 w-4"
          >
            <circle cx="11" cy="11" r="7" />
            <line x1="21" y1="21" x2="16.65" y2="16.65" />
          </svg>
        </button>
      </form>

      {/* Upload */}
      {user && (
        <button
          onClick={() => setShowUpload(true)}
          title="Upload post"
          className="shrink-0 flex items-center justify-center h-8 w-8 rounded-sm bg-(--color-accent) text-(--color-accent-text)"
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2.2"
            strokeLinecap="round"
            strokeLinejoin="round"
            className="h-4 w-4"
          >
            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
            <polyline points="17 8 12 3 7 8" />
            <line x1="12" y1="3" x2="12" y2="15" />
          </svg>
        </button>
      )}

      {/* Settings */}
      <SettingsMenu />

      {/* Auth */}
      <div className="shrink-0 text-sm">
        {user ? (
          <div className="flex items-center gap-3">
            {/* Username with hover dropdown */}
            <div className="relative group">
              <Link
                to={`/user/${user.name}`}
                className="text-(--color-text) hover:text-(--color-accent)"
              >
                {user.name}
              </Link>
              <div className="absolute right-0 top-full pt-1 hidden group-hover:block z-[200]">
                <div className="w-44 overflow-hidden rounded-[var(--radius-sm)] border border-(--color-border) bg-(--color-surface) shadow-xl">
                  {isAdmin(user) && (
                    <>
                      <div className="px-3 pt-2 pb-1 text-[10px] font-semibold uppercase tracking-wider text-(--color-muted)">
                        Admin
                      </div>
                      <Link
                        to="/admin"
                        className="flex w-full items-center gap-2 px-3 py-2 text-xs font-medium text-(--color-admin) transition-colors hover:bg-(--color-bg)"
                      >
                        Admin panel
                      </Link>
                      <div className="mx-2 my-1 border-t border-(--color-border)" />
                    </>
                  )}
                  <div className="px-3 pt-2 pb-1 text-[10px] font-semibold uppercase tracking-wider text-(--color-muted)">
                    Account
                  </div>
                  <button
                    onClick={logout}
                    className="flex w-full cursor-pointer items-center gap-2 px-3 py-2 text-left text-xs text-(--color-muted) transition-colors hover:bg-(--color-bg) hover:text-(--color-text)"
                  >
                    Sign out
                  </button>
                </div>
              </div>
            </div>
            {/* Envelope icon with unread badge */}
            <Link
              to="/messages"
              className="relative flex items-center text-(--color-muted) hover:text-(--color-text) transition-colors"
              title="Messages"
            >
              <svg
                xmlns="http://www.w3.org/2000/svg"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.8"
                strokeLinecap="round"
                strokeLinejoin="round"
                className="h-5 w-5"
              >
                <rect x="2" y="4" width="20" height="16" rx="2" />
                <path d="m22 7-10 7L2 7" />
              </svg>
              {unread > 0 && (
                <span className="absolute -top-1.5 -right-1.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-(--color-accent) px-0.5 text-[9px] font-bold leading-none text-white">
                  {unread > 99 ? "99+" : unread}
                </span>
              )}
            </Link>
          </div>
        ) : (
          <Link
            to="/login"
            className="text-(--color-muted) hover:text-(--color-text)"
          >
            login
          </Link>
        )}
      </div>

      {/* Drag-over overlay */}
      {dragging && (
        <div className="fixed inset-0 z-200 flex items-center justify-center bg-black/50 backdrop-blur-sm pointer-events-none">
          <div className="rounded-2xl border-2 border-dashed border-(--color-accent) px-12 py-10 text-center text-(--color-accent)">
            <div className="text-4xl mb-2">📎</div>
            <div className="text-lg font-semibold">Drop to upload</div>
          </div>
        </div>
      )}

      {showUpload && (
        <UploadModal
          initialFile={preloadFile}
          initialUrl={preloadUrl}
          onClose={() => {
            setShowUpload(false);
            setPreloadFile(null);
            setPreloadUrl("");
          }}
        />
      )}
    </nav>
  );
}
