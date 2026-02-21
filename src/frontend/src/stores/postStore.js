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

  // ── importFromPr0gramm ────────────────────────────────────────────────────
  // Streams the multi-phase pr0gramm import over SSE.
  //
  //   params     = { tags: string, flags: number, maxPages: number }
  //   onProgress = (event) => void
  //     event shapes (keyed by `phase`):
  //       { phase: 'fetching',    page, max_pages, total_read, at_end }
  //       { phase: 'inserted',    total, skipped_dedup }
  //       { phase: 'processing',  total, processed, imported, failed }
  //       { phase: 'done',        total, imported, failed }
  //       { phase: 'error',       message }
  //
  // Resolves with the final `done` event when the stream closes.
  // Rejects with an Error on network failure or if an `error` SSE event arrives.
  importFromPr0gramm: async ({ tags, flags = 1, maxPages = 5 }, onProgress) => {
    const resp = await fetch("/api/post/import/pr0gramm", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "include",
      body: JSON.stringify({ tags, flags, maxPages }),
    });

    if (!resp.ok) {
      let msg = `HTTP ${resp.status}`;
      try {
        const body = await resp.json();
        msg = body.error ?? body.message ?? msg;
      } catch (_) {}
      throw new Error(msg);
    }

    // Parse the SSE stream line-by-line.
    const reader = resp.body.getReader();
    const decoder = new TextDecoder();
    let buf = "";
    let finalEvent = null;

    while (true) {
      const { value, done } = await reader.read();
      if (done) break;

      buf += decoder.decode(value, { stream: true });

      // SSE events are separated by double newlines.
      const parts = buf.split("\n\n");
      // Keep the last (possibly incomplete) chunk in buf.
      buf = parts.pop() ?? "";

      for (const part of parts) {
        const dataLine = part.split("\n").find((l) => l.startsWith("data: "));
        if (!dataLine) continue;

        let event;
        try {
          event = JSON.parse(dataLine.slice(6)); // strip "data: "
        } catch (_) {
          continue;
        }

        onProgress?.(event);

        if (event.phase === "error") {
          throw new Error(event.message ?? "Import failed");
        }
        if (event.phase === "done") {
          finalEvent = event;
        }
      }
    }

    // After stream closes, refresh the post list.
    await get().fetchPosts({ reset: true });

    return finalEvent ?? { imported: 0, failed: 0, total: 0 };
  },

  clearListError: () => set({ listError: null }),
  clearPostError: () => set({ postError: null }),
}));

export default usePostStore;
