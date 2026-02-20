import { create } from "zustand";
import api from "../utils/api.js";

/**
 * Tag store — wraps:
 *   POST /api/tag/create  → 201 + { id, score, name, post_id, user_id }
 *   POST /api/tag/vote    → 200
 *
 * Tags for a post are fetched as part of GET /api/post/:id and seeded here.
 */
const useTagStore = create((set) => ({
  // Keyed by postId: { [postId]: PostTag[] }
  byPost: {},
  loading: false,
  error: null,

  // ── seed ──────────────────────────────────────────────────────────────────
  seed: (postId, tags) =>
    set((s) => ({ byPost: { ...s.byPost, [postId]: tags ?? [] } })),

  // ── createTag ─────────────────────────────────────────────────────────────
  createTag: async (postId, name) => {
    set({ loading: true, error: null });
    try {
      const { data } = await api.post("/tag/create", {
        post_id: postId,
        name,
      });
      set((s) => ({
        loading: false,
        byPost: {
          ...s.byPost,
          [postId]: [...(s.byPost[postId] ?? []), data],
        },
      }));
      return data;
    } catch (err) {
      set({ loading: false, error: err.message });
      throw err;
    }
  },

  // ── voteTag ───────────────────────────────────────────────────────────────
  // voteState: 1 = up, -1 = down, 0 = remove
  voteTag: async (postId, postTagId, voteState) => {
    try {
      await api.post("/tag/vote", {
        post_tag_id: postTagId,
        vote_state: voteState,
      });
      // Optimistic update
      set((s) => ({
        byPost: {
          ...s.byPost,
          [postId]: (s.byPost[postId] ?? []).map((t) =>
            t.id === postTagId ? { ...t, score: t.score + voteState } : t,
          ),
        },
      }));
    } catch (err) {
      throw err;
    }
  },

  clearError: () => set({ error: null }),
}));

export default useTagStore;
