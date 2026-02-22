import { useEffect, useMemo, useState } from "react";
import { useParams, Link } from "react-router-dom";
import usePostStore from "../stores/postStore.js";
import useAuthStore from "../stores/authStore.js";
import useInviteStore from "../stores/inviteStore.js";
import PostCard from "../components/PostCard.jsx";
import PostCardSkeleton from "../components/PostCardSkeleton.jsx";
import SectionHeader from "../components/SectionHeader.jsx";

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
      <span className="text-xl font-bold tabular-nums leading-none text-(--color-text)">
        {typeof value === "number" ? value.toLocaleString() : value}
      </span>
      <span className="text-xs uppercase tracking-wider text-(--color-muted)">
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
    <main className="pb-16">
      {/* ── HERO ──────────────────────────────────────────────────────────── */}
      <div className="border-b border-(--color-border)">
        {/* Name + level badge */}
        <div className="flex flex-wrap items-baseline gap-3 px-4 pt-5 pb-4">
          <h1 className="text-2xl font-bold tracking-tight text-(--color-accent)">
            {name}
          </h1>
          {profileLevel != null && (
            <span className="rounded border border-(--color-accent)/30 bg-(--color-accent)/10 px-2 py-0.5 text-xs font-semibold uppercase tracking-widest text-(--color-accent)">
              {levelLabel(profileLevel)}
            </span>
          )}
        </div>

        {/* Stats row */}
        <div className="flex flex-wrap gap-6 border-t border-(--color-border) px-4 py-3">
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

      {/* ── INVITATIONS (own profile only) ────────────────────────────────── */}
      {isOwn && (
        <section className="border-b border-(--color-border)">
          {/* Header row */}
          <div className="flex items-center justify-between px-4 py-3">
            <h2 className="text-xs font-semibold uppercase tracking-wide text-(--color-muted)">
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
              className="text-xs text-(--color-accent) hover:opacity-75 disabled:opacity-40"
            >
              {invLoading ? "…" : "+ generate"}
            </button>
          </div>

          {/* Copied URL toast */}
          {copiedToken && (
            <div className="flex items-center justify-between gap-3 border-t border-(--color-border) px-4 py-2 text-xs">
              <span className="truncate font-mono text-(--color-muted)">
                {window.location.origin}/register?invite={copiedToken}
              </span>
              <span className="shrink-0 font-semibold text-(--color-accent)">
                copied!
              </span>
            </div>
          )}

          {/* Empty state */}
          {invites.length === 0 && !invLoading && (
            <p className="border-t border-(--color-border) px-4 py-3 text-sm text-(--color-muted)">
              No invitations yet. Generate one to invite someone.
            </p>
          )}

          {/* Invite rows */}
          {invites.map((inv) => (
            <div
              key={inv.token}
              className="flex items-center justify-between gap-4 border-t border-(--color-border) px-4 py-2 text-xs"
            >
              <span className="min-w-0 flex-1 truncate font-mono text-(--color-muted)">
                {inv.token.slice(0, 8)}…
              </span>
              <span
                className={`shrink-0 font-semibold ${
                  inv.used_by != null
                    ? "text-(--color-muted)"
                    : "text-(--color-accent)"
                }`}
              >
                {inv.used_by != null ? "used" : "available"}
              </span>
              {inv.used_by == null && (
                <button
                  onClick={async () => {
                    const url = `${window.location.origin}/register?invite=${inv.token}`;
                    await navigator.clipboard.writeText(url);
                    setCopiedToken(inv.token);
                    setTimeout(() => setCopiedToken(null), 4000);
                  }}
                  className="shrink-0 text-(--color-accent) hover:opacity-75"
                >
                  copy
                </button>
              )}
            </div>
          ))}
        </section>
      )}

      {/* ── POSTS PREVIEW ─────────────────────────────────────────────────── */}
      <section className="px-4 pt-6">
        <SectionHeader title="Latest Posts" className="mb-3">
          {!listLoading && posts.length > 0 && (
            <Link
              to={`/user/${name}/posts`}
              className="text-xs text-(--color-accent) hover:opacity-75"
            >
              Browse all ↗
            </Link>
          )}
        </SectionHeader>

        {listError && (
          <p className="mb-4 rounded border border-(--color-danger)/50 bg-(--color-danger)/10 px-4 py-3 text-sm text-(--color-danger)">
            {listError}
          </p>
        )}

        {!listLoading && !listError && posts.length === 0 && (
          <div className="flex flex-col items-center gap-3 py-20 text-center">
            <span className="text-5xl opacity-20">📭</span>
            <p className="text-sm text-(--color-muted)">
              {isOwn
                ? "You haven't posted anything yet."
                : `${name} hasn't posted anything yet.`}
            </p>
          </div>
        )}

        <div className="grid gap-2 grid-cols-[repeat(auto-fill,minmax(160px,1fr))]">
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

      <Link
        to="/"
        className="mt-10 block px-4 text-sm text-(--color-accent) hover:opacity-75"
      >
        ← back
      </Link>
    </main>
  );
}
