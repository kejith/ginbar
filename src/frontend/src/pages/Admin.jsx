import { useEffect, useState, useCallback, useRef } from "react";
import api, { ssePost } from "../utils/api.js";
import {
  roleName,
  LEVEL_MEMBER,
  LEVEL_SECRET,
  LEVEL_ADMIN,
} from "../utils/roles.js";
import Tabs from "../components/Tabs.jsx";
import ProgressBar from "../components/ProgressBar.jsx";
import UserLink from "../components/UserLink.jsx";

// ── tiny helpers ─────────────────────────────────────────────────────────────

function fmtBytes(bytes) {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return `${(bytes / Math.pow(1024, i)).toFixed(1)} ${units[i]}`;
}

function fmtDuration(sec) {
  if (sec < 60) return `${sec}s`;
  if (sec < 3600) return `${Math.floor(sec / 60)}m ${sec % 60}s`;
  return `${Math.floor(sec / 3600)}h ${Math.floor((sec % 3600) / 60)}m`;
}

function StatCard({ label, value, sub }) {
  return (
    <div className="rounded-sm border border-(--color-border) bg-(--color-surface) p-4">
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
      ? "bg-(--color-admin) text-white"
      : level >= LEVEL_SECRET
        ? "bg-purple-600 text-white"
        : level >= LEVEL_MEMBER
          ? "bg-(--color-accent) text-(--color-accent-text)"
          : "bg-(--color-border) text-(--color-muted)";
  return (
    <span className={`rounded-sm px-2 py-0.5 text-xs font-semibold ${color}`}>
      {name} ({level})
    </span>
  );
}

// ── Process Queue card ────────────────────────────────────────────────────────
// Connects to the admin SSE stream and shows a live view of the processing queue.
// Always active — no button required.

function ProcessQueueCard() {
  const [snap, setSnap] = useState(null);
  const esRef = useRef(null);

  useEffect(() => {
    let retryTimer = null;

    function connect() {
      const es = new EventSource("/api/admin/queue/stream", {
        withCredentials: true,
      });
      esRef.current = es;

      es.addEventListener("message", (e) => {
        try {
          setSnap(JSON.parse(e.data));
        } catch (_) {}
      });

      es.addEventListener("error", () => {
        es.close();
        retryTimer = setTimeout(connect, 5000);
      });
    }

    connect();
    return () => {
      esRef.current?.close();
      clearTimeout(retryTimer);
    };
  }, []);

  const total = snap?.total ?? 0;
  const processed = snap?.processed ?? 0;
  const pending = snap?.pending ?? 0;
  const active = snap?.active ?? 0;
  const pct =
    total > 0 ? Math.round((processed / total) * 100) : snap?.running ? 0 : 100;

  return (
    <div className="rounded-sm border border-(--color-border) bg-(--color-surface) p-4">
      <div className="mb-3 flex items-center justify-between">
        <p className="text-sm font-semibold text-(--color-text)">
          Process Queue
        </p>
        <span
          className={`rounded-full px-2 py-0.5 text-xs font-medium ${
            snap?.running
              ? "bg-(--color-accent)/20 text-(--color-accent)"
              : "bg-(--color-border) text-(--color-muted)"
          }`}
        >
          {snap == null ? "connecting…" : snap.running ? "● active" : "○ idle"}
        </span>
      </div>

      {snap != null && (snap.running || total > 0) ? (
        <div className="space-y-2">
          <ProgressBar
            value={pct}
            status={snap.running ? "active" : "success"}
          />

          <div className="flex justify-between text-xs text-(--color-muted)">
            <span>
              {processed} / {total} processed
            </span>
            <span>{pending + active} remaining</span>
          </div>

          <div className="flex flex-wrap gap-3 text-xs">
            <span className="text-(--color-accent)">
              ✓ {snap.imported ?? 0} imported
            </span>
            <span className="text-(--color-danger)">
              ✗ {snap.failed ?? 0} failed
            </span>
            {snap.running && snap.rate_per_sec > 0 && (
              <span className="text-(--color-muted)">
                {snap.rate_per_sec.toFixed(1)}/s
              </span>
            )}
            {snap.running && snap.eta_sec >= 0 && (
              <span className="text-(--color-muted)">
                ~{fmtDuration(snap.eta_sec)} left
              </span>
            )}
          </div>
        </div>
      ) : snap != null ? (
        <p className="text-xs text-(--color-muted)">Queue is empty.</p>
      ) : null}
    </div>
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

  if (error) return <p className="text-(--color-danger) text-sm">{error}</p>;
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

      {/* Live process queue */}
      <ProcessQueueCard />

      {/* Disk usage */}
      <div className="rounded-sm border border-(--color-border) bg-(--color-surface) p-4">
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

  if (error) return <p className="text-(--color-danger) text-sm">{error}</p>;
  if (!users) return <p className="text-(--color-muted) text-sm">loading…</p>;

  return (
    <div className="overflow-x-auto rounded-sm border border-(--color-border)">
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
                      className="rounded-sm bg-(--color-admin) px-2 py-0.5 text-xs text-white disabled:opacity-50"
                    >
                      promote
                    </button>
                  ) : (
                    <button
                      disabled={busy === u.id}
                      onClick={() => setLevel(u.id, LEVEL_MEMBER)}
                      className="rounded-sm bg-(--color-border) px-2 py-0.5 text-xs text-(--color-text) disabled:opacity-50"
                    >
                      demote
                    </button>
                  )}
                  {u.level < LEVEL_SECRET ? (
                    <button
                      disabled={busy === u.id}
                      onClick={() => setLevel(u.id, LEVEL_SECRET)}
                      className="rounded-sm bg-purple-600 px-2 py-0.5 text-xs text-white disabled:opacity-50"
                    >
                      make secret
                    </button>
                  ) : u.level < LEVEL_ADMIN ? (
                    <button
                      disabled={busy === u.id}
                      onClick={() => setLevel(u.id, LEVEL_MEMBER)}
                      className="rounded-sm bg-(--color-border) px-2 py-0.5 text-xs text-(--color-text) disabled:opacity-50"
                    >
                      revoke secret
                    </button>
                  ) : null}
                  <button
                    disabled={busy === u.id}
                    onClick={() => deleteUser(u.id, u.name)}
                    className="rounded-sm bg-(--color-danger) px-2 py-0.5 text-xs text-white disabled:opacity-50"
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

  const contentTabs = [
    { id: "posts", label: posts ? `posts (${posts.length})` : "posts" },
    {
      id: "comments",
      label: comments ? `comments (${comments.length})` : "comments",
    },
    { id: "tags", label: tags ? `tags (${tags.length})` : "tags" },
    { id: "broadcast", label: "broadcast" },
  ];

  return (
    <div>
      <Tabs tabs={contentTabs} active={tab} onChange={setTab} />

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
                className="flex items-center gap-3 rounded-sm border border-(--color-border) bg-(--color-surface) p-2"
              >
                {p.thumbnail_filename && (
                  <img
                    src={`/images/thumbnails/${p.thumbnail_filename}`}
                    alt=""
                    className="h-10 w-10 shrink-0 rounded-sm object-cover"
                  />
                )}
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm text-(--color-text)">
                    #{p.id} —{" "}
                    <UserLink name={p.user_name} className="text-sm" />
                  </p>
                  <p className="truncate text-xs text-(--color-muted)">
                    {p.content_type} · score {p.score}
                  </p>
                </div>
                <button
                  disabled={busy === `post-${p.id}`}
                  onClick={() => deletePost(p.id)}
                  className="shrink-0 rounded-sm bg-(--color-danger) px-2 py-0.5 text-xs text-white disabled:opacity-50"
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
                className="flex items-start gap-3 rounded-sm border border-(--color-border) bg-(--color-surface) p-2"
              >
                <div className="min-w-0 flex-1">
                  <p className="text-xs text-(--color-muted) mb-0.5">
                    #{c.id} · post #{c.post_id} ·{" "}
                    <UserLink name={c.user_name} className="text-xs" />
                  </p>
                  <p className="text-sm text-(--color-text) line-clamp-2">
                    {c.content}
                  </p>
                </div>
                <button
                  disabled={busy === `comment-${c.id}`}
                  onClick={() => deleteComment(c.id)}
                  className="shrink-0 rounded-sm bg-(--color-danger) px-2 py-0.5 text-xs text-white disabled:opacity-50"
                >
                  delete
                </button>
              </div>
            ))
          )}
        </div>
      )}

      {/* Broadcast tab */}
      {tab === "broadcast" && <BroadcastPanel />}
    </div>
  );
}

// ── Broadcast panel ───────────────────────────────────────────────────────────

function BroadcastPanel() {
  const [subject, setSubject] = useState("");
  const [body, setBody] = useState("");
  const [status, setStatus] = useState(null); // null | 'loading' | { sent: N } | 'error'

  async function handleSubmit(e) {
    e.preventDefault();
    if (!body.trim()) return;
    setStatus("loading");
    try {
      const { data } = await api.post("/admin/message/broadcast", {
        subject: subject.trim() || undefined,
        body: body.trim(),
      });
      setStatus({ sent: data.sent });
      setBody("");
      setSubject("");
    } catch {
      setStatus("error");
    }
  }

  return (
    <div className="mt-2 p-3">
      <p className="text-xs text-(--color-muted) mb-3">
        Send a system message to every registered user.
      </p>
      <form onSubmit={handleSubmit} className="flex flex-col gap-2 max-w-md">
        <input
          value={subject}
          onChange={(e) => setSubject(e.target.value)}
          placeholder="Subject (optional)"
          className="rounded-sm bg-(--color-bg) px-3 py-2 text-sm text-(--color-text) placeholder:text-(--color-muted) ring-1 ring-(--color-border) focus:outline-none focus:ring-(--color-accent)"
        />
        <textarea
          value={body}
          onChange={(e) => setBody(e.target.value)}
          rows={4}
          placeholder="Message body…"
          required
          className="rounded-sm bg-(--color-bg) px-3 py-2 text-sm text-(--color-text) placeholder:text-(--color-muted) ring-1 ring-(--color-border) focus:outline-none focus:ring-(--color-accent) resize-none"
        />
        {status === "error" && (
          <p className="text-xs text-(--color-danger)">Broadcast failed.</p>
        )}
        {status && status !== "loading" && status !== "error" && (
          <p className="text-xs text-(--color-success)">
            Sent to {status.sent} user{status.sent !== 1 ? "s" : ""}.
          </p>
        )}
        <button
          type="submit"
          disabled={!body.trim() || status === "loading"}
          className="self-start rounded-sm bg-(--color-accent) px-4 py-1.5 text-sm font-medium text-(--color-accent-text) disabled:opacity-50"
        >
          {status === "loading" ? "sending…" : "broadcast"}
        </button>
      </form>
    </div>
  );
}

/**
 * JobCard — generic UI for one admin maintenance task.
 *
 * Props:
 *   job.id          string  — stable key
 *   job.label       string  — short title shown as heading
 *   job.description string  — one-line explanation
 *   job.run         async (onProgress) => result
 *                   onProgress: ({ current, total, message }) => void
 *                   Called zero or more times during execution for tasks that
 *                   can report incremental progress. For tasks that are a
 *                   single HTTP round-trip, simply don't call it.
 *   job.formatResult (result) => string | ReactNode
 *                   Converts the resolved value of run() into a human-readable
 *                   summary shown after completion.
 */
function JobCard({ job }) {
  const [state, setState] = useState("idle"); // idle | running | done | error
  const [progress, setProgress] = useState(null); // { current, total, message }
  const [result, setResult] = useState(null);
  const [errorMsg, setErrorMsg] = useState(null);
  const [finishedAt, setFinishedAt] = useState(null);
  const abortRef = useRef(false);

  function onProgress(p) {
    if (!abortRef.current) setProgress(p);
  }

  async function handleRun() {
    abortRef.current = false;
    setState("running");
    setProgress(null);
    setResult(null);
    setErrorMsg(null);
    setFinishedAt(null);
    try {
      const r = await job.run(onProgress);
      if (!abortRef.current) {
        setResult(r);
        setFinishedAt(new Date());
        setState("done");
      }
    } catch (e) {
      if (!abortRef.current) {
        setErrorMsg(e.message ?? "Unknown error");
        setState("error");
      }
    }
  }

  // Percentage helper — only shown when the job reports incremental progress
  const pct =
    progress?.total > 0
      ? Math.round((progress.current / progress.total) * 100)
      : null;

  return (
    <div className="rounded-lg border border-(--color-border) bg-(--color-surface) p-5 space-y-3">
      {/* Header */}
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <h3 className="font-semibold text-(--color-text)">{job.label}</h3>
          <p className="mt-0.5 text-sm text-(--color-muted)">
            {job.description}
          </p>
        </div>

        {/* Action button */}
        {state !== "running" && (
          <button
            onClick={handleRun}
            className="shrink-0 rounded-sm bg-(--color-accent) px-3 py-1.5 text-sm font-medium text-(--color-accent-text) hover:opacity-90 disabled:opacity-50 transition-opacity"
          >
            {state === "done" || state === "error" ? "Run again" : "Run"}
          </button>
        )}
        {state === "running" && (
          <span className="shrink-0 flex items-center gap-2 text-sm text-(--color-muted)">
            <svg
              className="h-4 w-4 animate-spin text-(--color-accent)"
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
            running…
          </span>
        )}
      </div>

      {/* Progress bar — only rendered when the job calls onProgress */}
      {state === "running" && progress !== null && (
        <div className="space-y-1">
          <ProgressBar value={pct ?? 0} status="active" height="md" />
          <div className="flex justify-between text-xs text-(--color-muted)">
            <span>{progress.message ?? ""}</span>
            <span>
              {progress.current} / {progress.total}
              {pct !== null ? ` (${pct}%)` : ""}
            </span>
          </div>
        </div>
      )}

      {/* Running — no incremental progress available */}
      {state === "running" && progress === null && (
        <ProgressBar value={100} status="pulse" height="sm" />
      )}

      {/* Result */}
      {state === "done" && result !== null && (
        <div className="rounded-sm bg-(--color-bg) border border-(--color-border) px-3 py-2 text-sm text-(--color-text) space-y-1">
          <div>{job.formatResult(result)}</div>
          {finishedAt && (
            <p className="text-xs text-(--color-muted)">
              Finished at {finishedAt.toLocaleTimeString()}
            </p>
          )}
        </div>
      )}

      {/* Error */}
      {state === "error" && (
        <p className="rounded-sm border border-(--color-danger)/50 bg-(--color-danger)/10 px-3 py-2 text-sm text-(--color-danger)">
          {errorMsg}
        </p>
      )}
    </div>
  );
}

// ── Job definitions ───────────────────────────────────────────────────────────

// Each entry is passed directly as the `job` prop to JobCard.
// To add a new job: define a new object following this shape and append it to
// the array. If the task can stream progress via SSE, call onProgress inside
// run(); if it's a single request, simply ignore onProgress.
const MAINTENANCE_JOBS = [
  {
    id: "load-new-pr0gramm",
    label: "Load 500 new posts from New",
    description:
      'Fetches the pr0gramm "New" feed (flags=1) page by page and inserts ' +
      "posts as dirty rows for the background queue to process. Stops once " +
      "500 new posts have been queued (or the feed is exhausted). " +
      "Duplicate URLs are skipped automatically.",
    run: async (onProgress) => {
      return ssePost("/admin/posts/load-new", {}, (event) => {
        if (event.phase === "progress") {
          onProgress({
            current: event.inserted_so_far,
            total: 500,
            message: `page ${event.page} · ${event.skipped_dedup} skipped · ${event.total_read} read`,
          });
        }
      });
    },
    formatResult: (r) => (
      <span>
        Queued <strong>{r.total}</strong> new post{r.total !== 1 ? "s" : ""}{" "}
        across <strong>{r.pages_read}</strong> page
        {r.pages_read !== 1 ? "s" : ""} ({r.total_read} items read).
        {r.skipped_dedup > 0 && (
          <span className="text-(--color-muted)">
            {" "}
            {r.skipped_dedup} already exist.
          </span>
        )}
        {r.filtered_ext > 0 && (
          <span className="text-(--color-muted)">
            {" "}
            {r.filtered_ext} unsupported format.
          </span>
        )}
        {r.total >= 500 && (
          <span className="text-(--color-success)"> Target reached ✓</span>
        )}
      </span>
    ),
  },
  {
    id: "backfill-dimensions",
    label: "Backfill post dimensions",
    description:
      "Reads the real width & height from every image/video file on disk for " +
      "posts that were uploaded before dimension tracking was added. " +
      "Safe to run multiple times — only affects posts where width = 0.",
    run: async (_onProgress) => {
      const r = await api.post("/admin/posts/backfill-dimensions");
      return r.data;
    },
    formatResult: (r) => {
      const remaining = (r.total ?? 0) - (r.updated ?? 0) - (r.failed ?? 0);
      return (
        <span>
          Updated <strong>{r.updated}</strong> of <strong>{r.total}</strong>{" "}
          posts.
          {r.failed > 0 && (
            <span className="text-(--color-danger)"> {r.failed} failed.</span>
          )}
          {remaining <= 0 ? (
            <span className="text-(--color-success)">
              {" "}
              All posts have dimensions ✓
            </span>
          ) : (
            <span className="text-(--color-muted)">
              {" "}
              {remaining} remaining.
            </span>
          )}
        </span>
      );
    },
  },
  {
    id: "regenerate-images",
    label: "Regenerate images as AVIF",
    description:
      "Re-encodes every stored image as a high-quality AVIF (CRF 18 for " +
      "full-res, CRF 30 for thumbnails) and replaces old files on disk. " +
      "Safe to re-run. Large libraries will take a while.",
    run: async (onProgress) => {
      return ssePost("/admin/posts/regenerate-images", {}, (event) => {
        if (event.phase === "start") {
          onProgress({ current: 0, total: event.total, message: "Starting…" });
        } else if (event.phase === "progress") {
          onProgress({
            current: event.current,
            total: event.total,
            message: `updated ${event.updated} · failed ${event.failed} · skipped ${event.skipped}`,
          });
        }
      });
    },
    formatResult: (r) => (
      <span>
        Re-encoded <strong>{r.updated}</strong> of <strong>{r.total}</strong>{" "}
        images.
        {r.failed > 0 && (
          <span className="text-(--color-danger)"> {r.failed} failed.</span>
        )}
        {r.skipped > 0 && (
          <span className="text-(--color-muted)">
            {" "}
            {r.skipped} skipped (file not found).
          </span>
        )}
        {r.failed === 0 && r.skipped === 0 && r.updated === r.total && (
          <span className="text-(--color-success)">
            {" "}
            All images regenerated ✓
          </span>
        )}
      </span>
    ),
  },
];

function MaintenanceSection() {
  return (
    <div className="space-y-4">
      <p className="text-sm text-(--color-muted)">
        One-off administrative tasks. Each job is idempotent and safe to re-run.
      </p>
      {MAINTENANCE_JOBS.map((job) => (
        <JobCard key={job.id} job={job} />
      ))}
    </div>
  );
}

// ── Page ──────────────────────────────────────────────────────────────────────

export default function Admin() {
  const [section, setSection] = useState("stats");

  const SECTIONS = [
    { id: "stats", label: "stats" },
    { id: "users", label: "users" },
    { id: "content", label: "content" },
    { id: "maintenance", label: "maintenance" },
  ];

  return (
    <main className="mx-auto max-w-5xl px-4 py-6">
      <h1 className="mb-6 text-2xl font-bold text-(--color-text)">
        Admin Panel
      </h1>

      {/* Section nav */}
      <Tabs
        tabs={SECTIONS}
        active={section}
        onChange={setSection}
        className="mb-6"
      />

      {/* Section content */}
      {section === "stats" && <StatsSection />}
      {section === "users" && <UsersSection />}
      {section === "content" && <ContentSection />}
      {section === "maintenance" && <MaintenanceSection />}
    </main>
  );
}
