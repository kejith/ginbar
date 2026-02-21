import { useRef, useState, useEffect } from "react";
import {
  Link,
  useNavigate,
  useSearchParams,
  useLocation,
} from "react-router-dom";
import useAuthStore from "../stores/authStore.js";
import UploadModal from "./UploadModal.jsx";
import { isAdmin } from "../utils/roles.js";

export default function Nav() {
  const user = useAuthStore((s) => s.user);
  const logout = useAuthStore((s) => s.logout);
  const inputRef = useRef(null);
  const navigate = useNavigate();
  const [showUpload, setShowUpload] = useState(false);
  const [searchParams] = useSearchParams();
  const location = useLocation();

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
      <Link
        to="/"
        className="shrink-0 text-lg font-bold tracking-tight text-(--color-accent)"
      >
        ginbar
      </Link>

      {/* Search */}
      <form onSubmit={handleSearch} className="flex min-w-0 flex-1 gap-2">
        <input
          ref={inputRef}
          type="search"
          placeholder="tags… or user:name…"
          className="h-8 w-full min-w-0 rounded bg-(--color-bg) px-3 text-sm text-(--color-text) placeholder:text-(--color-muted) outline-none ring-1 ring-(--color-border) focus:ring-(--color-accent)"
        />
        <button
          type="submit"
          className="shrink-0 rounded bg-(--color-accent) px-3 text-sm font-medium text-white"
        >
          go
        </button>
      </form>

      {/* Auth */}
      <div className="shrink-0 text-sm">
        {user ? (
          <div className="flex items-center gap-3">
            <button
              onClick={() => setShowUpload(true)}
              className="rounded bg-(--color-accent) px-2.5 py-1 text-xs font-semibold text-white"
              title="Upload post"
            >
              + post
            </button>
            {isAdmin(user) && (
              <Link
                to="/admin"
                className="rounded bg-amber-600 px-2.5 py-1 text-xs font-semibold text-white"
                title="Admin panel"
              >
                admin
              </Link>
            )}
            <Link
              to={`/user/${user.name}`}
              className="text-(--color-text) hover:text-(--color-accent)"
            >
              {user.name}
            </Link>
            <button
              onClick={logout}
              className="text-(--color-muted) hover:text-(--color-text)"
            >
              out
            </button>
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

      {showUpload && <UploadModal onClose={() => setShowUpload(false)} />}
    </nav>
  );
}
