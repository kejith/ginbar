import { create } from "zustand";
import api from "../utils/api.js";

/**
 * Message store.
 *
 * State:
 *   unread              number               total unread badge count
 *   notifications       EnrichedNotification[] paginated list
 *   notificationsPage   number               current page loaded
 *   notificationsHasMore bool                whether more pages exist
 *   conversations       ConversationItem[]   private thread summaries
 *   thread              Message[]            active private thread messages
 *   activePartner       string | null        username of open conversation
 *   loading             bool
 *   error               string | null
 *
 * EnrichedNotification shape from API:
 *   { id, kind, from_name, to_name, body, read_at, created_at,
 *     ref_post_id, ref_comment_id,
 *     ref_post_thumbnail, ref_comment_user_name, ref_comment_score,
 *     ref_comment_created_at, ref_comment_content }
 *
 * ConversationItem shape (built on the server):
 *   { partner, last_at, unread, last_body }
 */
const useMessageStore = create((set, get) => ({
  unread: 0,
  notifications: [],
  notificationsPage: 0,
  notificationsHasMore: true,
  conversations: [],
  thread: [],
  activePartner: null,
  loading: false,
  error: null,

  // ── fetchUnread ───────────────────────────────────────────────────────────
  fetchUnread: async () => {
    try {
      const { data } = await api.get("/message/unread");
      set({ unread: data.count ?? 0 });
    } catch {
      // silently ignore — badge just won't update
    }
  },

  // ── fetchInbox ────────────────────────────────────────────────────────────
  fetchInbox: async () => {
    set({ loading: true, error: null });
    try {
      const { data } = await api.get("/message/inbox");
      set({
        conversations: data.conversations ?? [],
        loading: false,
      });
    } catch (err) {
      set({ loading: false, error: err.message });
    }
  },

  // ── fetchNotifications ────────────────────────────────────────────────────
  // Appends the given page to the notifications list (page 1 replaces).
  fetchNotifications: async (page = 1) => {
    set({ loading: true, error: null });
    try {
      const { data } = await api.get(`/message/notifications?page=${page}`);
      const incoming = data.notifications ?? [];
      set((s) => ({
        notifications:
          page === 1 ? incoming : [...s.notifications, ...incoming],
        notificationsPage: page,
        notificationsHasMore: data.has_more ?? false,
        loading: false,
      }));
    } catch (err) {
      set({ loading: false, error: err.message });
    }
  },

  // ── openThread ────────────────────────────────────────────────────────────
  openThread: async (partner) => {
    set({ activePartner: partner, thread: [], loading: true, error: null });
    try {
      const { data } = await api.get(`/message/thread/${partner}`);
      const msgs = data.messages ?? [];
      set({ thread: msgs, loading: false });

      // Locally clear unread count for this partner in conversations.
      set((s) => ({
        conversations: s.conversations.map((c) =>
          c.partner === partner ? { ...c, unread: 0 } : c,
        ),
        // Recalculate total unread.
        unread: Math.max(
          0,
          s.unread -
            (s.conversations.find((c) => c.partner === partner)?.unread ?? 0),
        ),
      }));
    } catch (err) {
      set({ loading: false, error: err.message });
    }
  },

  // ── sendMessage ───────────────────────────────────────────────────────────
  sendMessage: async (toName, body) => {
    set({ loading: true, error: null });
    try {
      const { data } = await api.post("/message/send", {
        to_name: toName,
        body,
      });
      set((s) => {
        // Append message to active thread.
        const newThread = [...s.thread, data];

        // Update or create the conversation entry.
        const existing = s.conversations.find((c) => c.partner === toName);
        let conversations;
        if (existing) {
          conversations = s.conversations.map((c) =>
            c.partner === toName
              ? { ...c, last_at: data.created_at, last_body: data.body }
              : c,
          );
        } else {
          conversations = [
            {
              partner: toName,
              last_at: data.created_at,
              last_body: data.body,
              unread: 0,
            },
            ...s.conversations,
          ];
        }

        return { thread: newThread, conversations, loading: false };
      });
    } catch (err) {
      set({ loading: false, error: err.message });
      throw err;
    }
  },

  // ── markAllRead ───────────────────────────────────────────────────────────
  markAllRead: async () => {
    try {
      await api.post("/message/mark-all-read");
      set((s) => ({
        unread: 0,
        notifications: s.notifications.map((n) => ({
          ...n,
          read_at: n.read_at ?? new Date().toISOString(),
        })),
        conversations: s.conversations.map((c) => ({ ...c, unread: 0 })),
      }));
    } catch {
      // ignore
    }
  },

  clearError: () => set({ error: null }),
}));

export default useMessageStore;
