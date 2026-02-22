import { useEffect, useState, useRef } from "react";
import { useNavigate } from "react-router-dom";
import useMessageStore from "../stores/messageStore.js";
import useAuthStore from "../stores/authStore.js";
import UserLink from "../components/UserLink.jsx";
import { timeAgo } from "../utils/timeAgo.js";
import api from "../utils/api.js";

// ── Helpers ──────────────────────────────────────────────────────────────────

function ts(isoOrObj) {
  if (!isoOrObj) return "";
  const d = new Date(
    typeof isoOrObj === "object" ? isoOrObj.Time ?? isoOrObj : isoOrObj,
  );
  return isNaN(d) ? "" : d.toLocaleString();
}

// ── NotificationRow ───────────────────────────────────────────────────────────

function NotificationRow({ n, onMarkRead }) {
  const navigate = useNavigate();
  const unread = !n.read_at;

  const hasPostRef = n.ref_post_id != null;
  const hasCommentRef = n.ref_comment_id != null;

  const thumbUrl = n.ref_post_thumbnail
    ? `/images/thumbnails/${n.ref_post_thumbnail}`
    : null;

  const deepLink = hasPostRef
    ? `/post/${n.ref_post_id}${hasCommentRef ? `?comment=${n.ref_comment_id}` : ""}`
    : null;

  async function handleClick() {
    if (unread) await onMarkRead(n.id);
    if (deepLink) navigate(deepLink);
  }

  return (
    <div
      className={`flex gap-3 px-4 py-3 rounded-lg cursor-pointer transition-colors hover:bg-(--color-surface) ${
        unread
          ? "border-l-2 border-(--color-accent)"
          : "border-l-2 border-transparent"
      }`}
      onClick={handleClick}
      role="button"
      tabIndex={0}
      onKeyDown={(e) => e.key === "Enter" && handleClick()}
      aria-label={unread ? "Unread notification" : "Notification"}
    >
      {/* Thumbnail */}
      <div className="shrink-0 w-12 h-12 rounded-[var(--radius-sm)] overflow-hidden bg-(--color-border) flex items-center justify-center">
        {thumbUrl ? (
          <img
            src={thumbUrl}
            alt="post thumbnail"
            className="w-full h-full object-cover"
            onClick={(e) => {
              e.stopPropagation();
              if (deepLink) navigate(deepLink);
            }}
          />
        ) : (
          <svg
            className="w-6 h-6 text-(--color-muted)"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth={1.5}
              d="M15 17h5l-1.405-1.405A2.032 2.032 0 0118 14.158V11a6.002 6.002 0 00-4-5.659V5a2 2 0 10-4 0v.341C7.67 6.165 6 8.388 6 11v3.159c0 .538-.214 1.055-.595 1.436L4 17h5m6 0v1a3 3 0 11-6 0v-1m6 0H9"
            />
          </svg>
        )}
      </div>

      {/* Content */}
      <div className="flex-1 min-w-0">
        {n.ref_comment_content ? (
          <p className="text-sm text-(--color-text) line-clamp-2 mb-1">
            {n.ref_comment_content}
          </p>
        ) : (
          <p className="text-sm text-(--color-text) line-clamp-2 mb-1">
            {n.body}
          </p>
        )}

        {n.ref_comment_user_name ? (
          <p className="text-xs text-(--color-muted)">
            Comment from{" "}
            <span onClick={(e) => e.stopPropagation()} className="inline">
              <UserLink name={n.ref_comment_user_name} />
            </span>
            {" · "}
            {n.ref_comment_score != null && (
              <>↑{n.ref_comment_score}{" · "}</>
            )}
            {n.ref_comment_created_at
              ? timeAgo(n.ref_comment_created_at)
              : timeAgo(n.created_at)}
          </p>
        ) : (
          <p className="text-xs text-(--color-muted)">
            {n.from_name ? (
              <>
                <span onClick={(e) => e.stopPropagation()} className="inline">
                  <UserLink name={n.from_name} />
                </span>
                {" · "}
              </>
            ) : (
              "System · "
            )}
            {ts(n.created_at)}
          </p>
        )}
      </div>

      {/* Unread dot */}
      {unread && (
        <div className="shrink-0 mt-2 w-2 h-2 rounded-full bg-(--color-accent)" />
      )}
    </div>
  );
}

// ── ThreadPanel (DM chat) ─────────────────────────────────────────────────────

function ThreadPanel({ partner }) {
  const { thread, loading, sendMessage } = useMessageStore();
  const user = useAuthStore((s) => s.user);
  const [body, setBody] = useState("");
  const [sending, setSending] = useState(false);
  const [sendError, setSendError] = useState("");
  const bottomRef = useRef(null);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [thread]);

  async function handleSend(e) {
    e.preventDefault();
    if (!body.trim()) return;
    setSending(true);
    setSendError("");
    try {
      await sendMessage(partner, body.trim());
      setBody("");
    } catch (err) {
      setSendError(err.message);
    } finally {
      setSending(false);
    }
  }

  return (
    <div className="flex flex-col h-full">
      <div className="px-4 py-3 border-b border-(--color-border) shrink-0">
        <p className="font-medium text-(--color-text)">
          <UserLink name={partner} />
        </p>
      </div>

      <div className="flex-1 overflow-y-auto px-4 py-3 flex flex-col gap-2">
        {loading && thread.length === 0 && (
          <p className="text-sm text-(--color-muted) text-center py-8">Loading…</p>
        )}
        {!loading && thread.length === 0 && (
          <p className="text-sm text-(--color-muted) text-center py-8">
            No messages yet — say hello!
          </p>
        )}
        {thread.map((m) => {
          const mine = m.from_name === user?.name;
          return (
            <div
              key={m.id}
              className={`flex ${mine ? "justify-end" : "justify-start"}`}
            >
              <div
                className={`max-w-[70%] px-3 py-2 rounded-2xl text-sm ${
                  mine
                    ? "bg-(--color-accent) text-(--color-accent-text) rounded-br-sm"
                    : "bg-(--color-surface) text-(--color-text) rounded-bl-sm"
                }`}
              >
                <p className="wrap-break-word whitespace-pre-wrap">{m.body}</p>
                <p
                  className={`text-[10px] mt-1 ${
                    mine ? "text-white/60" : "text-(--color-muted)"
                  }`}
                >
                  {ts(m.created_at)}
                </p>
              </div>
            </div>
          );
        })}
        <div ref={bottomRef} />
      </div>

      <form
        onSubmit={handleSend}
        className="px-4 py-3 border-t border-(--color-border) shrink-0 flex gap-2"
      >
        <input
          className="flex-1 rounded-full border border-(--color-border) bg-(--color-bg) px-4 py-2 text-sm text-(--color-text) focus:outline-none focus:border-(--color-accent)"
          placeholder={`Message ${partner}…`}
          value={body}
          onChange={(e) => setBody(e.target.value)}
          disabled={sending}
        />
        <button
          type="submit"
          disabled={sending || !body.trim()}
          className="px-4 py-2 rounded-full bg-(--color-accent) text-(--color-accent-text) text-sm font-medium disabled:opacity-40 hover:opacity-90 transition-opacity"
        >
          Send
        </button>
      </form>
      {sendError && (
        <p className="px-4 pb-2 text-xs text-(--color-danger)">{sendError}</p>
      )}
    </div>
  );
}

// ── Main Messages page ────────────────────────────────────────────────────────

export default function Messages() {
  const user = useAuthStore((s) => s.user);
  const {
    notifications,
    notificationsPage,
    notificationsHasMore,
    conversations,
    activePartner,
    loading,
    fetchNotifications,
    fetchInbox,
    openThread,
    markAllRead,
  } = useMessageStore();

  const [activeTab, setActiveTab] = useState("notifications");
  const [newDmPartner, setNewDmPartner] = useState("");
  const [showNewDm, setShowNewDm] = useState(false);
  const [markingRead, setMarkingRead] = useState(false);

  useEffect(() => {
    if (!user) return;
    fetchNotifications(1);
    fetchInbox();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [user]);

  async function handleMarkRead(id) {
    try {
      await api.post("/message/mark-read", { id });
      useMessageStore.setState((s) => ({
        notifications: s.notifications.map((n) =>
          n.id === id ? { ...n, read_at: new Date().toISOString() } : n,
        ),
        unread: Math.max(0, s.unread - 1),
      }));
    } catch {
      // ignore
    }
  }

  async function handleMarkAllRead() {
    setMarkingRead(true);
    try {
      await markAllRead();
    } finally {
      setMarkingRead(false);
    }
  }

  function handleLoadMore() {
    fetchNotifications(notificationsPage + 1);
  }

  function handleOpenDm(partner) {
    openThread(partner);
  }

  function handleNewDmSubmit(e) {
    e.preventDefault();
    const partner = newDmPartner.trim();
    if (!partner) return;
    openThread(partner);
    setNewDmPartner("");
    setShowNewDm(false);
  }

  if (!user) {
    return (
      <div className="flex items-center justify-center h-full">
        <p className="text-(--color-muted)">Please log in to view messages.</p>
      </div>
    );
  }

  const unreadCount = notifications.filter((n) => !n.read_at).length;

  return (
    <div
      className="flex flex-col"
      style={{ height: "calc(100vh - var(--nav-height))" }}
    >
      {/* Tab bar */}
      <div className="flex items-center gap-1 px-4 pt-4 pb-0 border-b border-(--color-border) shrink-0">
        <button
          onClick={() => setActiveTab("notifications")}
          className={`relative px-4 py-2 text-sm font-medium transition-colors ${
            activeTab === "notifications"
              ? "text-(--color-accent) border-b-2 border-(--color-accent)"
              : "text-(--color-muted) hover:text-(--color-text)"
          }`}
        >
          Notifications
          {unreadCount > 0 && (
            <span className="ml-2 inline-flex items-center justify-center text-[10px] font-bold w-4 h-4 rounded-full bg-(--color-accent) text-(--color-accent-text)">
              {unreadCount > 99 ? "99+" : unreadCount}
            </span>
          )}
        </button>
        <button
          onClick={() => setActiveTab("dms")}
          className={`px-4 py-2 text-sm font-medium transition-colors ${
            activeTab === "dms"
              ? "text-(--color-accent) border-b-2 border-(--color-accent)"
              : "text-(--color-muted) hover:text-(--color-text)"
          }`}
        >
          Direct Messages
        </button>
      </div>

      {/* Notifications tab */}
      {activeTab === "notifications" && (
        <div className="flex-1 overflow-y-auto">
          <div className="flex justify-end px-4 pt-3 pb-1">
            {unreadCount > 0 && (
              <button
                onClick={handleMarkAllRead}
                disabled={markingRead}
                className="text-xs text-(--color-muted) hover:text-(--color-text) transition-colors disabled:opacity-50"
              >
                {markingRead ? "Marking…" : "Mark all read"}
              </button>
            )}
          </div>

          {loading && notifications.length === 0 && (
            <p className="text-sm text-(--color-muted) text-center py-16">
              Loading notifications…
            </p>
          )}
          {!loading && notifications.length === 0 && (
            <p className="text-sm text-(--color-muted) text-center py-16">
              No notifications yet.
            </p>
          )}

          <div className="flex flex-col gap-1 px-2 pb-4">
            {notifications.map((n) => (
              <NotificationRow key={n.id} n={n} onMarkRead={handleMarkRead} />
            ))}
          </div>

          {notificationsHasMore && (
            <div className="flex justify-center py-4">
              <button
                onClick={handleLoadMore}
                disabled={loading}
                className="px-6 py-2 rounded-full border border-(--color-border) text-sm text-(--color-text) hover:bg-(--color-surface) transition-colors disabled:opacity-50"
              >
                {loading ? "Loading…" : "Load more"}
              </button>
            </div>
          )}
        </div>
      )}

      {/* Direct Messages tab */}
      {activeTab === "dms" && (
        <div className="flex flex-1 min-h-0">
          {/* Sidebar */}
          <aside className="w-64 border-r border-(--color-border) flex flex-col shrink-0 overflow-y-auto">
            <div className="p-3 border-b border-(--color-border)">
              {showNewDm ? (
                <form onSubmit={handleNewDmSubmit} className="flex gap-2">
                  <input
                    autoFocus
                    className="flex-1 rounded-[var(--radius-sm)] border border-(--color-border) bg-(--color-bg) px-2 py-1 text-sm text-(--color-text) focus:outline-none focus:border-(--color-accent)"
                    placeholder="Username…"
                    value={newDmPartner}
                    onChange={(e) => setNewDmPartner(e.target.value)}
                  />
                  <button
                    type="submit"
                    className="px-2 py-1 rounded-[var(--radius-sm)] bg-(--color-accent) text-(--color-accent-text) text-sm"
                  >
                    Go
                  </button>
                  <button
                    type="button"
                    onClick={() => setShowNewDm(false)}
                    className="px-2 py-1 rounded-[var(--radius-sm)] border border-(--color-border) text-sm text-(--color-muted)"
                  >
                    ✕
                  </button>
                </form>
              ) : (
                <button
                  onClick={() => setShowNewDm(true)}
                  className="w-full text-sm text-center py-1.5 rounded-[var(--radius-sm)] border border-(--color-border) text-(--color-muted) hover:text-(--color-text) hover:bg-(--color-surface) transition-colors"
                >
                  + New message
                </button>
              )}
            </div>

            {conversations.length === 0 && (
              <p className="text-xs text-(--color-muted) text-center py-6 px-3">
                No conversations yet.
              </p>
            )}
            {conversations.map((c) => (
              <button
                key={c.partner}
                onClick={() => handleOpenDm(c.partner)}
                className={`w-full flex items-start gap-2 px-3 py-2.5 text-left transition-colors hover:bg-(--color-surface) ${
                  activePartner === c.partner ? "bg-(--color-surface)" : ""
                }`}
              >
                <div className="flex-1 min-w-0">
                  <p className="text-sm font-medium text-(--color-text) truncate">
                    {c.partner}
                  </p>
                  <p className="text-xs text-(--color-muted) truncate">
                    {c.last_body}
                  </p>
                </div>
                {c.unread > 0 && (
                  <span className="shrink-0 mt-0.5 inline-flex items-center justify-center text-[10px] font-bold min-w-[1rem] h-4 px-1 rounded-full bg-(--color-accent) text-(--color-accent-text)">
                    {c.unread}
                  </span>
                )}
              </button>
            ))}
          </aside>

          {/* Right panel */}
          <div className="flex-1 min-w-0">
            {activePartner ? (
              <ThreadPanel partner={activePartner} />
            ) : (
              <div className="flex items-center justify-center h-full">
                <p className="text-sm text-(--color-muted)">
                  Select a conversation or start a new one.
                </p>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
