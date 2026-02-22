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

  // All tags globally (for autocomplete suggestions)
  allTags: [],
  allTagsLoaded: false,

  // ── seed ──────────────────────────────────────────────────────────────────
  seed: (postId, tags) =>
    set((s) => ({ byPost: { ...s.byPost, [postId]: tags ?? [] } })),

  // ── fetchAllTags ──────────────────────────────────────────────────────────
  // Fetches all tags once and caches them for autocomplete.
  fetchAllTags: async () => {
    if (useTagStore.getState().allTagsLoaded) return;
    try {
      const { data } = await api.get("/tag/");
      set({ allTags: data.tags ?? [], allTagsLoaded: true });
    } catch {
      // non-fatal — suggestions just won't appear
    }
  },

  // ── createTag ─────────────────────────────────────────────────────────────
  createTag: async (postId, name) => {
    set({ loading: true, error: null });
    try {
      const { data } = await api.post("/tag/create", {
        post_id: postId,
        name,
      });
      set((s) => {
        const tagName =
          typeof data.name === "object" ? data.name.String : data.name;
        const alreadyKnown = s.allTags.some((t) => {
          const n = typeof t.name === "object" ? t.name.String : t.name;
          return n === tagName;
        });
        return {
          loading: false,
          byPost: {
            ...s.byPost,
            [postId]: [...(s.byPost[postId] ?? []), data],
          },
          allTags: alreadyKnown
            ? s.allTags
            : [...s.allTags, { id: data.id, name: data.name }],
        };
      });
      return data;
    } catch (err) {
      set({ loading: false, error: err.message });
      throw err;
    }
  },

  // ── voteTag ───────────────────────────────────────────────────────────────
  // voteState: 1 = up, -1 = down, 0 = remove
  voteTag: async (postId, postTagId, voteState) => {
    // Capture old tag state for rollback
    const tags = useTagStore.getState().byPost[postId] ?? [];
    const oldTag = tags.find((t) => t.id === postTagId);
    const oldVote = oldTag?.vote ?? 0;
    const delta = voteState - oldVote;

    // Optimistic update — instant
    set((s) => ({
      byPost: {
        ...s.byPost,
        [postId]: (s.byPost[postId] ?? []).map((t) =>
          t.id !== postTagId
            ? t
            : { ...t, score: t.score + delta, vote: voteState },
        ),
      },
    }));

    try {
      await api.post("/tag/vote", {
        post_tag_id: postTagId,
        vote_state: voteState,
      });
    } catch (err) {
      // Revert on failure
      set((s) => ({
        byPost: {
          ...s.byPost,
          [postId]: (s.byPost[postId] ?? []).map((t) =>
            t.id !== postTagId
              ? t
              : { ...t, score: t.score - delta, vote: oldVote },
          ),
        },
      }));
      throw err;
    }
  },

  clearError: () => set({ error: null }),
}));

export default useTagStore;
