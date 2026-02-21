import { useEffect, useMemo, useState } from "react";
import { useParams, Link } from "react-router-dom";
import usePostStore from "../stores/postStore.js";
import useAuthStore from "../stores/authStore.js";
import useInviteStore from "../stores/inviteStore.js";
import PostCard from "../components/PostCard.jsx";
import PostCardSkeleton from "../components/PostCardSkeleton.jsx";

// ── helpers ───────────────────────────────────────────────────────────────────

function levelLabel(level) {
  if (level >= 10) return "Admin";
  if (level >= 5) return "Mod";
  if (level >= 3) return "Trusted";
  return "User";
}

/** "2 years", "8 months", etc. — duration since the given ISO timestamp. */
function memberDuration(iso) {
  if (!iso) return "";
  const secs = Math.floor((Date.now() - new Date(iso).getTime()) / 1000);
  if (isNaN(secs) || secs < 0) return "";
  const days = Math.floor(secs / 86400);
  if (days < 30) return `${days} day${days !== 1 ? "s" : ""}`;
  const months = Math.floor(days / 30);
  if (months < 12) return `${months} month${months !== 1 ? "s" : ""}`;
  const years = Math.floor(months / 12);
  return `${years} year${years !== 1 ? "s" : ""}`;
}

// ── sub-components ────────────────────────────────────────────────────────────

function StatBlock({ label, value }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span
        className="text-xl font-bold tabular-nums leading-none"
        style={{ color: "var(--color-text)" }}
      >
        {typeof value === "number" ? value.toLocaleString() : value}
      </span>
      <span
        className="text-xs uppercase tracking-wider"
        style={{ color: "var(--color-muted)" }}
      >
        {label}
      </span>
    </div>
  );
}

// ── main component ────────────────────────────────────────────────────────────
// Route: /user/:name
// Shows the profile hero card + a 33-post preview grid.
// Clicking a post thumbnail navigates to /user/:name/:postId (UserGrid).

export default function Profile() {
  const { name } = useParams();
  const currentUser = useAuthStore((s) => s.user);
  const { posts, listLoading, listError, fetchPostsByUser } = usePostStore();
  const {
    invites,
    loading: invLoading,
    createInvite,
    fetchInvites,
  } = useInviteStore();
  const [copiedToken, setCopiedToken] = useState(null);

  useEffect(() => {
    fetchPostsByUser(name);
  }, [name]);

  const isOwn = currentUser && currentUser.name === name;

  useEffect(() => {
    if (isOwn) fetchInvites();
  }, [isOwn]);
  const profileLevel = isOwn ? currentUser.level : null;

  const stats = useMemo(() => {
    if (!posts.length) return null;
    const totalScore = posts.reduce((sum, p) => sum + (p.score ?? 0), 0);
    const oldest = posts.reduce(
      (min, p) => (p.created_at && p.created_at < min ? p.created_at : min),
      posts[0]?.created_at ?? "",
    );
    return { postCount: posts.length, totalScore, oldestPost: oldest };
  }, [posts]);

  const visiblePosts = posts.slice(0, 33);
  const skeletons = Array.from({ length: 14 });

  return (
    <main className="p-4 pb-16">
      {/* ── HERO ──────────────────────────────────────────────────────────── */}
      <div
        className="relative mb-8 overflow-hidden rounded-xl border p-6"
        style={{
          borderColor: "var(--color-border)",
          background: "var(--color-surface)",
        }}
      >
        {/* coloured top stripe */}
        <div
          className="absolute inset-x-0 top-0 h-1 rounded-t-xl"
          style={{ background: "var(--color-accent)" }}
        />

        <div className="flex flex-wrap items-start gap-6 pt-2">
          <div className="flex min-w-0 flex-1 flex-col gap-4">
            {/* Name + level badge */}
            <div className="flex flex-wrap items-baseline gap-3">
              <h1
                className="truncate text-2xl font-bold tracking-tight"
                style={{ color: "var(--color-text)" }}
              >
                {name}
              </h1>
              {profileLevel != null && (
                <span
                  className="rounded px-2 py-0.5 text-xs font-semibold uppercase tracking-widest"
                  style={{
                    background: "rgba(249,115,22,0.13)",
                    color: "var(--color-accent)",
                    border: "1px solid rgba(249,115,22,0.33)",
                  }}
                >
                  {levelLabel(profileLevel)}
                </span>
              )}
            </div>

            {/* Stats row */}
            <div
              className="flex flex-wrap gap-6 border-t pt-4"
              style={{ borderColor: "var(--color-border)" }}
            >
              <StatBlock
                label="Posts"
                value={listLoading ? "—" : (stats?.postCount ?? 0)}
              />
              <StatBlock
                label="Upvotes"
                value={listLoading ? "—" : (stats?.totalScore ?? 0)}
              />
              {!listLoading && stats?.oldestPost && (
                <StatBlock
                  label="Member since"
                  value={memberDuration(stats.oldestPost)}
                />
              )}
            </div>
          </div>
        </div>
      </div>

      {/* ── POSTS PREVIEW ─────────────────────────────────────────────────── */}
      <section>
        <div
          className="mb-3 flex items-center justify-between border-b pb-2"
          style={{ borderColor: "var(--color-border)" }}
        >
          <h2
            className="text-xs font-semibold uppercase tracking-widest"
            style={{ color: "var(--color-muted)" }}
          >
            Latest Posts
          </h2>
          {!listLoading && posts.length > 0 && (
            <Link
              to={`/user/${name}/posts`}
              className="text-xs hover:opacity-80"
              style={{ color: "var(--color-accent)" }}
            >
              Browse all ↗
            </Link>
          )}
        </div>

        {listError && (
          <p className="mb-4 rounded border border-red-800 bg-red-950/40 px-4 py-3 text-sm text-red-400">
            {listError}
          </p>
        )}

        {!listLoading && !listError && posts.length === 0 && (
          <div className="flex flex-col items-center gap-3 py-20 text-center">
            <span className="text-5xl opacity-20">📭</span>
            <p className="text-sm" style={{ color: "var(--color-muted)" }}>
              {isOwn
                ? "You haven't posted anything yet."
                : `${name} hasn't posted anything yet.`}
            </p>
          </div>
        )}

        <div
          className="grid gap-2"
          style={{
            gridTemplateColumns: "repeat(auto-fill, minmax(160px, 1fr))",
          }}
        >
          {listLoading
            ? skeletons.map((_, i) => <PostCardSkeleton key={i} />)
            : visiblePosts.map((post) => (
                <PostCard
                  key={post.id}
                  post={post}
                  onExpand={(id) =>
                    window.location.assign(`/user/${name}/posts/${id}`)
                  }
                  isExpanded={false}
                />
              ))}
        </div>
      </section>

      {/* ── INVITATIONS (own profile only) ────────────────────────────────── */}
      {isOwn && (
        <section className="mt-10">
          <div
            className="mb-3 flex items-center justify-between border-b pb-2"
            style={{ borderColor: "var(--color-border)" }}
          >
            <h2
              className="text-xs font-semibold uppercase tracking-widest"
              style={{ color: "var(--color-muted)" }}
            >
              Invitations
            </h2>
            <button
              onClick={async () => {
                try {
                  const token = await createInvite();
                  const url = `${window.location.origin}/register?invite=${token}`;
                  await navigator.clipboard.writeText(url);
                  setCopiedToken(token);
                  setTimeout(() => setCopiedToken(null), 4000);
                } catch {
                  // ignore
                }
              }}
              disabled={invLoading}
              className="rounded bg-(--color-accent) px-3 py-1 text-xs font-semibold text-white disabled:opacity-50"
            >
              {invLoading ? "…" : "Generate invite link"}
            </button>
          </div>

          {copiedToken && (
            <div
              className="mb-3 flex items-center justify-between gap-3 rounded border px-3 py-2 text-xs"
              style={{
                borderColor: "var(--color-border)",
                background: "var(--color-surface)",
                color: "var(--color-text)",
              }}
            >
              <span className="truncate font-mono">
                {window.location.origin}/register?invite={copiedToken}
              </span>
              <span
                className="shrink-0 font-semibold"
                style={{ color: "var(--color-accent)" }}
              >
                copied!
              </span>
            </div>
          )}

          {invites.length === 0 && !invLoading && (
            <p className="text-sm" style={{ color: "var(--color-muted)" }}>
              No invitations yet. Generate one to invite someone.
            </p>
          )}

          <ul className="flex flex-col gap-1">
            {invites.map((inv) => (
              <li
                key={inv.token}
                className="flex items-center justify-between gap-4 rounded border px-3 py-2 text-xs"
                style={{
                  borderColor: "var(--color-border)",
                  background: "var(--color-surface)",
                }}
              >
                <span
                  className="truncate font-mono"
                  style={{ color: "var(--color-muted)" }}
                >
                  {inv.token.slice(0, 8)}…
                </span>
                <span
                  className="shrink-0 font-semibold"
                  style={{
                    color:
                      inv.used_by !== null && inv.used_by !== undefined
                        ? "var(--color-muted)"
                        : "var(--color-accent)",
                  }}
                >
                  {inv.used_by !== null && inv.used_by !== undefined
                    ? "used"
                    : "available"}
                </span>
                {(inv.used_by === null || inv.used_by === undefined) && (
                  <button
                    onClick={async () => {
                      const url = `${window.location.origin}/register?invite=${inv.token}`;
                      await navigator.clipboard.writeText(url);
                      setCopiedToken(inv.token);
                      setTimeout(() => setCopiedToken(null), 4000);
                    }}
                    className="shrink-0 underline hover:opacity-80"
                    style={{ color: "var(--color-accent)" }}
                  >
                    copy
                  </button>
                )}
              </li>
            ))}
          </ul>
        </section>
      )}

      <Link
        to="/"
        className="mt-10 inline-block text-sm underline hover:opacity-80"
        style={{ color: "var(--color-muted)" }}
      >
        ← back
      </Link>
    </main>
  );
}
