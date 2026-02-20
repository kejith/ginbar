import { useRef } from "react";
import { Link, useNavigate } from "react-router-dom";
import useAuthStore from "../stores/authStore.js";

export default function Nav() {
  const user = useAuthStore((s) => s.user);
  const logout = useAuthStore((s) => s.logout);
  const inputRef = useRef(null);
  const navigate = useNavigate();

  function handleSearch(e) {
    e.preventDefault();
    const q = inputRef.current?.value.trim();
    if (q) navigate(`/?q=${encodeURIComponent(q)}`);
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
          placeholder="search tags…"
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
    </nav>
  );
}
