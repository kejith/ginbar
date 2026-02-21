import { useEffect, useState, useCallback } from "react";
import api from "../utils/api.js";
import { roleName, LEVEL_MEMBER, LEVEL_ADMIN } from "../utils/roles.js";

// ── tiny helpers ─────────────────────────────────────────────────────────────

function fmtBytes(bytes) {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return `${(bytes / Math.pow(1024, i)).toFixed(1)} ${units[i]}`;
}

function StatCard({ label, value, sub }) {
  return (
    <div className="rounded border border-(--color-border) bg-(--color-surface) p-4">
      <p className="text-2xl font-bold text-(--color-text)">{value ?? "—"}</p>
      <p className="mt-1 text-sm text-(--color-muted)">{label}</p>
      {sub && <p className="mt-0.5 text-xs text-(--color-muted)">{sub}</p>}
    </div>
  );
}

function RoleBadge({ level }) {
  const name = roleName(level);
  const color =
    level >= LEVEL_ADMIN
      ? "bg-amber-600 text-white"
      : level >= LEVEL_MEMBER
        ? "bg-(--color-accent) text-white"
        : "bg-(--color-border) text-(--color-muted)";
  return (
    <span className={`rounded px-2 py-0.5 text-xs font-semibold ${color}`}>
      {name} ({level})
    </span>
  );
}

// ── Section: Stats ────────────────────────────────────────────────────────────

function StatsSection() {
  const [stats, setStats] = useState(null);
  const [error, setError] = useState(null);

  useEffect(() => {
    api
      .get("/admin/stats")
      .then((r) => setStats(r.data))
      .catch((e) => setError(e.message));
  }, []);

  if (error) return <p className="text-red-400 text-sm">{error}</p>;
  if (!stats) return <p className="text-(--color-muted) text-sm">loading…</p>;

  const { counts, disk } = stats;

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
        <StatCard label="posts" value={counts.posts} />
        <StatCard label="comments" value={counts.comments} />
        <StatCard label="tags" value={counts.tags} />
        <StatCard label="users" value={counts.users} />
        <StatCard
          label="pending import"
          value={counts.pending_import}
          sub="dirty posts"
        />
      </div>

      {/* Disk usage */}
      <div className="rounded border border-(--color-border) bg-(--color-surface) p-4">
        <p className="mb-3 text-sm font-semibold text-(--color-text)">
          Disk usage — total: {fmtBytes(disk.total_bytes)}
        </p>
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-(--color-muted)">
              <th className="pb-1 pr-4 font-medium">category</th>
              <th className="pb-1 pr-4 font-medium">files</th>
              <th className="pb-1 font-medium">size</th>
            </tr>
          </thead>
          <tbody>
            {disk.breakdown.map((d) => (
              <tr key={d.label} className="border-t border-(--color-border)">
                <td className="py-1.5 pr-4 text-(--color-text)">{d.label}</td>
                <td className="py-1.5 pr-4 text-(--color-muted)">{d.files}</td>
                <td className="py-1.5 text-(--color-text)">
                  {fmtBytes(d.bytes)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

// ── Section: Users ────────────────────────────────────────────────────────────

function UsersSection() {
  const [users, setUsers] = useState(null);
  const [error, setError] = useState(null);
  const [busy, setBusy] = useState(null); // id of user being mutated

  const load = useCallback(() => {
    api
      .get("/admin/users")
      .then((r) => setUsers(r.data.users))
      .catch((e) => setError(e.message));
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  async function setLevel(id, level) {
    setBusy(id);
    try {
      await api.patch(`/admin/users/${id}/level`, { level });
      load();
    } catch (e) {
      alert(e.message);
    } finally {
      setBusy(null);
    }
  }

  async function deleteUser(id, name) {
    if (!confirm(`Delete user "${name}"? This cannot be undone.`)) return;
    setBusy(id);
    try {
      await api.delete(`/admin/users/${id}`);
      load();
    } catch (e) {
      alert(e.message);
    } finally {
      setBusy(null);
    }
  }

  if (error) return <p className="text-red-400 text-sm">{error}</p>;
  if (!users) return <p className="text-(--color-muted) text-sm">loading…</p>;

  return (
    <div className="overflow-x-auto rounded border border-(--color-border)">
      <table className="w-full text-sm">
        <thead className="bg-(--color-surface)">
          <tr className="text-left text-(--color-muted)">
            <th className="p-3 font-medium">id</th>
            <th className="p-3 font-medium">name</th>
            <th className="p-3 font-medium">email</th>
            <th className="p-3 font-medium">role</th>
            <th className="p-3 font-medium">joined</th>
            <th className="p-3 font-medium">actions</th>
          </tr>
        </thead>
        <tbody>
          {users.map((u) => (
            <tr
              key={u.id}
              className="border-t border-(--color-border) hover:bg-(--color-surface)/50"
            >
              <td className="p-3 text-(--color-muted)">{u.id}</td>
              <td className="p-3 font-medium text-(--color-text)">{u.name}</td>
              <td className="p-3 text-(--color-muted)">{u.email}</td>
              <td className="p-3">
                <RoleBadge level={u.level} />
              </td>
              <td className="p-3 text-(--color-muted)">
                {new Date(u.created_at).toLocaleDateString()}
              </td>
              <td className="p-3">
                <div className="flex flex-wrap gap-1">
                  {u.level < LEVEL_ADMIN ? (
                    <button
                      disabled={busy === u.id}
                      onClick={() => setLevel(u.id, LEVEL_ADMIN)}
                      className="rounded bg-amber-600 px-2 py-0.5 text-xs text-white disabled:opacity-50"
                    >
                      promote
                    </button>
                  ) : (
                    <button
                      disabled={busy === u.id}
                      onClick={() => setLevel(u.id, LEVEL_MEMBER)}
                      className="rounded bg-(--color-border) px-2 py-0.5 text-xs text-(--color-text) disabled:opacity-50"
                    >
                      demote
                    </button>
                  )}
                  <button
                    disabled={busy === u.id}
                    onClick={() => deleteUser(u.id, u.name)}
                    className="rounded bg-red-700 px-2 py-0.5 text-xs text-white disabled:opacity-50"
                  >
                    delete
                  </button>
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// ── Section: Content moderation ───────────────────────────────────────────────

function ContentSection() {
  const [tab, setTab] = useState("posts");
  const [posts, setPosts] = useState(null);
  const [comments, setComments] = useState(null);
  const [tags, setTags] = useState(null);
  const [busy, setBusy] = useState(null);

  useEffect(() => {
    api
      .get("/post?limit=100")
      .then((r) => setPosts(r.data.posts ?? []))
      .catch(() => setPosts([]));
    api
      .get("/admin/comments")
      .then((r) => setComments(r.data.comments ?? []))
      .catch(() => setComments([]));
    api
      .get("/tag/")
      .then((r) => setTags(r.data.tags ?? []))
      .catch(() => setTags([]));
  }, []);

  async function deletePost(id) {
    if (!confirm(`Delete post #${id}?`)) return;
    setBusy(`post-${id}`);
    try {
      await api.delete(`/admin/posts/${id}`);
      setPosts((p) => p.filter((x) => x.id !== id));
    } catch (e) {
      alert(e.message);
    } finally {
      setBusy(null);
    }
  }

  async function deleteComment(id) {
    setBusy(`comment-${id}`);
    try {
      await api.delete(`/admin/comments/${id}`);
      setComments((c) => c.filter((x) => x.id !== id));
    } catch (e) {
      alert(e.message);
    } finally {
      setBusy(null);
    }
  }

  async function deleteTag(id, name) {
    setBusy(`tag-${id}`);
    try {
      await api.delete(`/admin/tags/${id}`);
      setTags((t) => t.filter((x) => x.id !== id));
    } catch (e) {
      alert(e.message);
    } finally {
      setBusy(null);
    }
  }

  const tabClass = (t) =>
    `px-3 py-1.5 text-sm font-medium rounded-t border-b-2 ${
      tab === t
        ? "border-(--color-accent) text-(--color-accent)"
        : "border-transparent text-(--color-muted) hover:text-(--color-text)"
    }`;

  return (
    <div>
      {/* Tabs */}
      <div className="flex gap-1 border-b border-(--color-border)">
        <button className={tabClass("posts")} onClick={() => setTab("posts")}>
          posts {posts && `(${posts.length})`}
        </button>
        <button
          className={tabClass("comments")}
          onClick={() => setTab("comments")}
        >
          comments {comments && `(${comments.length})`}
        </button>
        <button className={tabClass("tags")} onClick={() => setTab("tags")}>
          tags {tags && `(${tags.length})`}
        </button>
      </div>

      {/* Posts tab */}
      {tab === "posts" && (
        <div className="mt-2 space-y-1">
          {!posts ? (
            <p className="text-(--color-muted) text-sm p-2">loading…</p>
          ) : posts.length === 0 ? (
            <p className="text-(--color-muted) text-sm p-2">no posts</p>
          ) : (
            posts.map((p) => (
              <div
                key={p.id}
                className="flex items-center gap-3 rounded border border-(--color-border) bg-(--color-surface) p-2"
              >
                {p.thumbnail_filename && (
                  <img
                    src={`/images/thumbnails/${p.thumbnail_filename}`}
                    alt=""
                    className="h-10 w-10 shrink-0 rounded object-cover"
                  />
                )}
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm text-(--color-text)">
                    #{p.id} — {p.user_name}
                  </p>
                  <p className="truncate text-xs text-(--color-muted)">
                    {p.content_type} · score {p.score}
                  </p>
                </div>
                <button
                  disabled={busy === `post-${p.id}`}
                  onClick={() => deletePost(p.id)}
                  className="shrink-0 rounded bg-red-700 px-2 py-0.5 text-xs text-white disabled:opacity-50"
                >
                  delete
                </button>
              </div>
            ))
          )}
        </div>
      )}

      {/* Comments tab */}
      {tab === "comments" && (
        <div className="mt-2 space-y-1">
          {!comments ? (
            <p className="text-(--color-muted) text-sm p-2">loading…</p>
          ) : comments.length === 0 ? (
            <p className="text-(--color-muted) text-sm p-2">no comments</p>
          ) : (
            comments.map((c) => (
              <div
                key={c.id}
                className="flex items-start gap-3 rounded border border-(--color-border) bg-(--color-surface) p-2"
              >
                <div className="min-w-0 flex-1">
                  <p className="text-xs text-(--color-muted) mb-0.5">
                    #{c.id} · post #{c.post_id} · {c.user_name}
                  </p>
                  <p className="text-sm text-(--color-text) line-clamp-2">
                    {c.content}
                  </p>
                </div>
                <button
                  disabled={busy === `comment-${c.id}`}
                  onClick={() => deleteComment(c.id)}
                  className="shrink-0 rounded bg-red-700 px-2 py-0.5 text-xs text-white disabled:opacity-50"
                >
                  delete
                </button>
              </div>
            ))
          )}
        </div>
      )}

      {/* Tags tab */}
      {tab === "tags" && (
        <div className="mt-2">
          {!tags ? (
            <p className="text-(--color-muted) text-sm p-2">loading…</p>
          ) : tags.length === 0 ? (
            <p className="text-(--color-muted) text-sm p-2">no tags</p>
          ) : (
            <div className="flex flex-wrap gap-2 p-2">
              {tags.map((t) => (
                <div
                  key={t.id}
                  className="flex items-center gap-1.5 rounded border border-(--color-border) bg-(--color-surface) px-2 py-1 text-sm"
                >
                  <span className="text-(--color-text)">{t.name}</span>
                  <button
                    disabled={busy === `tag-${t.id}`}
                    onClick={() => deleteTag(t.id, t.name)}
                    className="text-red-500 hover:text-red-400 disabled:opacity-50"
                    title="delete tag"
                  >
                    ×
                  </button>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// ── Page ──────────────────────────────────────────────────────────────────────

export default function Admin() {
  const [section, setSection] = useState("stats");

  const navClass = (s) =>
    `px-4 py-2 text-sm font-medium rounded ${
      section === s
        ? "bg-(--color-accent) text-white"
        : "text-(--color-muted) hover:text-(--color-text)"
    }`;

  return (
    <main className="mx-auto max-w-5xl px-4 py-6">
      <h1 className="mb-6 text-2xl font-bold text-(--color-text)">
        Admin Panel
      </h1>

      {/* Section nav */}
      <div className="mb-6 flex flex-wrap gap-2">
        <button
          className={navClass("stats")}
          onClick={() => setSection("stats")}
        >
          stats
        </button>
        <button
          className={navClass("users")}
          onClick={() => setSection("users")}
        >
          users
        </button>
        <button
          className={navClass("content")}
          onClick={() => setSection("content")}
        >
          content
        </button>
      </div>

      {/* Section content */}
      {section === "stats" && <StatsSection />}
      {section === "users" && <UsersSection />}
      {section === "content" && <ContentSection />}
    </main>
  );
}
