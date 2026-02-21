import { create } from "zustand";
import api from "../utils/api.js";

/**
 * Post store — wraps:
 *   GET  /api/post/*             → { posts }
 *   GET  /api/post/search/:q     → { posts }
 *   GET  /api/post/:id           → { data, comments, tags }
 *   POST /api/post/vote          → 200
 *   POST /api/post/create        → post
 *   POST /api/post/upload        → post (multipart)
 */
const usePostStore = create((set, get) => ({
  // ── list state ────────────────────────────────────────────────────────────
  posts: [],
  page: 1,
  hasMore: true,
  listLoading: false,
  listError: null,

  // ── single post state ─────────────────────────────────────────────────────
  current: null, // { data, comments, tags }
  postLoading: false,
  postError: null,

  // ── fetchPosts (paginated) ────────────────────────────────────────────────
  fetchPosts: async ({ page = 1, limit = 50, tag, reset = false } = {}) => {
    set({ listLoading: true, listError: null });
    try {
      const params = { page, limit };
      if (tag) params.tag = tag;
      const { data } = await api.get("/post/", { params });
      const incoming = data.posts ?? [];
      set((s) => ({
        posts: reset ? incoming : [...s.posts, ...incoming],
        page,
        hasMore: incoming.length === limit,
        listLoading: false,
      }));
    } catch (err) {
      set({ listLoading: false, listError: err.message });
    }
  },

  // ── search ────────────────────────────────────────────────────────────────
  // Tags are space-separated; pass as a plain string — the store handles encoding.
  search: async (query) => {
    set({ listLoading: true, listError: null });
    try {
      const encoded = encodeURIComponent(query.trim()).replace(/%20/g, "%20");
      const { data } = await api.get(`/post/search/${encoded}`);
      set({ posts: data.posts ?? [], listLoading: false, hasMore: false });
    } catch (err) {
      set({ listLoading: false, listError: err.message });
    }
  },

  // ── fetchPost (single) ────────────────────────────────────────────────────
  fetchPost: async (id) => {
    set({ postLoading: true, postError: null, current: null });
    try {
      const { data } = await api.get(`/post/${id}`);
      set({ current: data, postLoading: false });
    } catch (err) {
      set({ postLoading: false, postError: err.message });
    }
  },

  // ── vote ──────────────────────────────────────────────────────────────────
  // voteState: 1 = up, -1 = down, 0 = remove
  votePost: async (postId, voteState) => {
    // Capture previous state for rollback
    const prev = get();
    const oldVoteInList = prev.posts.find((p) => p.id === postId)?.vote ?? 0;
    const delta = voteState - oldVoteInList;
    const oldCurrentVote =
      prev.current?.data?.id === postId ? (prev.current.data.vote ?? 0) : null;

    // Optimistic update — instant
    set((s) => ({
      posts: s.posts.map((p) =>
        p.id === postId ? { ...p, score: p.score + delta, vote: voteState } : p,
      ),
      current:
        s.current?.data?.id === postId
          ? {
              ...s.current,
              data: {
                ...s.current.data,
                score:
                  s.current.data.score +
                  (voteState - (s.current.data.vote ?? 0)),
                vote: voteState,
              },
            }
          : s.current,
    }));

    try {
      await api.post("/post/vote", { post_id: postId, vote_state: voteState });
    } catch (err) {
      // Revert on failure
      set((s) => ({
        posts: s.posts.map((p) =>
          p.id === postId
            ? { ...p, score: p.score - delta, vote: oldVoteInList }
            : p,
        ),
        current:
          oldCurrentVote !== null
            ? {
                ...s.current,
                data: {
                  ...s.current.data,
                  score: s.current.data.score - (voteState - oldCurrentVote),
                  vote: oldCurrentVote,
                },
              }
            : s.current,
      }));
      throw err;
    }
  },

  // ── fetchPostsByUser ──────────────────────────────────────────────────────
  fetchPostsByUser: async (username) => {
    set({ listLoading: true, listError: null });
    try {
      const { data } = await api.get("/post/", { params: { username } });
      set({ posts: data.posts ?? [], listLoading: false, hasMore: false });
    } catch (err) {
      set({ listLoading: false, listError: err.message });
    }
  },

  // ── createPost (URL-based) ────────────────────────────────────────────────
  createPost: async (url) => {
    const { data } = await api.post("/post/create", { URL: url });
    const post = data.posts?.[0];
    if (post) set((s) => ({ posts: [post, ...s.posts] }));
    return data;
  },

  // ── uploadPost (file) ─────────────────────────────────────────────────────
  uploadPost: async (file) => {
    const form = new FormData();
    form.append("file", file);
    const { data } = await api.post("/post/upload", form, {
      headers: { "Content-Type": "multipart/form-data" },
    });
    const post = data.posts?.[0];
    if (post) set((s) => ({ posts: [post, ...s.posts] }));
    return data;
  },

  clearListError: () => set({ listError: null }),
  clearPostError: () => set({ postError: null }),
}));

export default usePostStore;
