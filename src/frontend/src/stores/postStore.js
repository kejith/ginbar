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

  // ── filter state ─────────────────────────────────────────────────────────
  // Toggleable extra filters. SFW is always included automatically.
  // Possible values: 'nsfp' | 'nsfw' | 'secret'
  activeFilters: ["nsfp"],

  /** Replace the active feed filters and reset the post list. */
  setFilters: (filters) =>
    set({ activeFilters: filters, posts: [], page: 1, hasMore: true }),

  // ── cursor-mode state (active when a direct /post/:id URL is opened) ──────
  // Replaces page-based pagination for the default feed. Both ends use the
  // min/max id already in `posts` as the cursor so no extra tracking needed.
  cursorMode: false,
  hasOlderPosts: true,
  hasNewerPosts: false,
  olderLoading: false,
  newerLoading: false,

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
      const { activeFilters } = usePostStore.getState();
      const filterParam = ["sfw", ...activeFilters].join(",");
      if (filterParam) params.filter = filterParam;
      const { data } = await api.get("/post/", { params });
      const incoming = data.posts ?? [];
      set((s) => ({
        posts: reset ? incoming : [...s.posts, ...incoming],
        page,
        hasMore: incoming.length === limit,
        listLoading: false,
        // exit cursor mode when doing a full reset
        ...(reset
          ? { cursorMode: false, hasNewerPosts: false, hasOlderPosts: true }
          : {}),
      }));
    } catch (err) {
      set({ listLoading: false, listError: err.message });
    }
  },

  // ── fetchAroundPost ───────────────────────────────────────────────────────
  // For direct /post/:id URLs: loads a window of posts centered on the target
  // in a single round trip. The backend returns has_newer / has_older flags so
  // bi-directional cursor scroll works from the first render.
  fetchAroundPost: async (postId) => {
    set({ listLoading: true, listError: null, cursorMode: false });
    try {
      const { activeFilters } = usePostStore.getState();
      const filterParam = ["sfw", ...activeFilters].join(",");
      const params = filterParam ? { filter: filterParam } : undefined;
      const { data } = await api.get(`/post/around/${postId}`, { params });
      const incoming = data.posts ?? [];
      set({
        posts: incoming,
        listLoading: false,
        cursorMode: true,
        hasNewerPosts: data.has_newer ?? false,
        hasOlderPosts: data.has_older ?? false,
        olderLoading: false,
        newerLoading: false,
      });
    } catch (err) {
      set({ listLoading: false, listError: err.message });
    }
  },

  // ── loadOlderPosts ────────────────────────────────────────────────────────
  // Appends posts older than the current oldest post (cursor = min id in list).
  loadOlderPosts: async () => {
    const { posts, hasOlderPosts, olderLoading } = usePostStore.getState();
    if (!hasOlderPosts || olderLoading || posts.length === 0) return;
    const minId = Math.min(...posts.map((p) => p.id));
    set({ olderLoading: true });
    try {
      const { activeFilters } = usePostStore.getState();
      const cursorParams = { before_id: minId };
      const filterParam = ["sfw", ...activeFilters].join(",");
      if (filterParam) cursorParams.filter = filterParam;
      const { data } = await api.get("/post/cursor", { params: cursorParams });
      const incoming = data.posts ?? [];
      set((s) => ({
        posts: [...s.posts, ...incoming],
        hasOlderPosts: data.has_more ?? false,
        olderLoading: false,
      }));
    } catch (err) {
      set({ olderLoading: false, listError: err.message });
    }
  },

  // ── loadNewerPosts ────────────────────────────────────────────────────────
  // Prepends posts newer than the current newest post (cursor = max id in list).
  loadNewerPosts: async () => {
    const { posts, hasNewerPosts, newerLoading } = usePostStore.getState();
    if (!hasNewerPosts || newerLoading || posts.length === 0) return;
    const maxId = Math.max(...posts.map((p) => p.id));
    set({ newerLoading: true });
    try {
      const { activeFilters } = usePostStore.getState();
      const cursorParams = { after_id: maxId };
      const filterParam = ["sfw", ...activeFilters].join(",");
      if (filterParam) cursorParams.filter = filterParam;
      const { data } = await api.get("/post/cursor", { params: cursorParams });
      const incoming = data.posts ?? [];
      set((s) => ({
        posts: [...incoming, ...s.posts],
        hasNewerPosts: data.has_more ?? false,
        newerLoading: false,
      }));
    } catch (err) {
      set({ newerLoading: false, listError: err.message });
    }
  },

  // ── search ────────────────────────────────────────────────────────────────
  // Tags are space-separated; pass as a plain string — the store handles encoding.
  // Optional username filters results on the backend.
  search: async (query, username) => {
    set({ listLoading: true, listError: null });
    try {
      const encoded = encodeURIComponent(query.trim()).replace(/%20/g, "%20");
      const { activeFilters } = usePostStore.getState();
      const filterParam = ["sfw", ...activeFilters].join(",");
      const params = {};
      if (username) params.user = username;
      if (filterParam) params.filter = filterParam;
      const { data } = await api.get(`/post/search/${encoded}`, {
        params: Object.keys(params).length ? params : undefined,
      });
      set({
        posts: data.posts ?? [],
        listLoading: false,
        hasMore: false,
        cursorMode: false,
        hasNewerPosts: false,
        hasOlderPosts: false,
      });
    } catch (err) {
      set({ listLoading: false, listError: err.message });
    }
  },

  // ── fetchPost (single) ────────────────────────────────────────────────────
  fetchPost: async (id) => {
    // Keep stale `current` visible while the new fetch is in-flight so the
    // metadata panel (votes, uploader, comments) doesn't flash away during
    // post-to-post navigation. `current` is replaced once the new response
    // arrives; `isReady` in InlinePost guards against rendering stale data
    // because it checks `current.data?.id === postId`.
    set({ postLoading: true, postError: null });
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
  // Returns { status: "queued", post_id, queue_position, eta_sec }.
  // Poll getPostQueueStatus(post_id) until dirty=false to know when it is done.
  // Filter starts as "sfw" — add tag "nsfp", "nsfw", or "secret" to promote it.
  createPost: async (url) => {
    const { data } = await api.post("/post/create", { URL: url });
    return data; // { status, post_id, queue_position, eta_sec }
  },

  // ── uploadPost (file) ─────────────────────────────────────────────────────
  // Returns { status: "queued", post_id, queue_position, eta_sec }.
  // Filter starts as "sfw" — add tag "nsfp", "nsfw", or "secret" to promote it.
  uploadPost: async (file) => {
    const form = new FormData();
    form.append("file", file);
    const { data } = await api.post("/post/upload", form, {
      headers: { "Content-Type": "multipart/form-data" },
    });
    return data; // { status, post_id, queue_position, eta_sec }
  },

  // ── getUserQueueStatus ────────────────────────────────────────────────────
  // Returns { has_post, post_id?, queue_position?, eta_sec? }.
  // has_post=false means the user has no post currently in the queue.
  getUserQueueStatus: async () => {
    const { data } = await api.get("/post/my-queue");
    return data;
  },

  // ── getPostQueueStatus ────────────────────────────────────────────────────
  // Returns { dirty, needs_release, queue_position, eta_sec }.
  // dirty=false means the post was finalized (or deleted on failure).
  // needs_release=true means the post is done processing but not yet released.
  getPostQueueStatus: async (postId) => {
    const { data } = await api.get(`/post/queue/${postId}`);
    return data;
  },

  // ── releasePost ───────────────────────────────────────────────────────────
  // Publishes a finalized post: adds tags, creates an optional initial comment,
  // and sets released=true so the post becomes visible in the feed.
  releasePost: async (postId, tags, comment) => {
    const { data } = await api.post(`/post/${postId}/release`, {
      tags: tags ?? [],
      comment: comment ?? "",
    });
    return data;
  },

  // ── importFromPr0gramm ────────────────────────────────────────────────────
  // Streams the page-fetch phase of a pr0gramm import over SSE.
  // Processing is handled by the background queue worker (visible in admin panel).
  //
  //   params     = { tags: string, flags: number, maxPages: number }
  //   onProgress = (event) => void
  //     event shapes (keyed by `phase`):
  //       { phase: 'fetching',  page, max_pages, total_read, at_end }
  //       { phase: 'inserted',  total, filtered_ext, skipped_dedup, insert_errors }
  //       { phase: 'done',      total, imported: 0, failed: 0 }
  //       { phase: 'error',     message }
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

  // ── removePost (admin) ───────────────────────────────────────────────────
  removePost: (postId) =>
    set((s) => ({ posts: s.posts.filter((p) => p.id !== postId) })),

  clearListError: () => set({ listError: null }),
  clearPostError: () => set({ postError: null }),
}));

export default usePostStore;
