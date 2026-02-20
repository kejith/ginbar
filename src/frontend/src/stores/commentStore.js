import { create } from 'zustand'
import api from '../utils/api.js'

/**
 * Comment store — wraps:
 *   POST /api/comment/create  → 201 + comment
 *   POST /api/comment/vote    → 200
 *
 * Comments for a post are fetched as part of GET /api/post/:id and injected
 * here by postStore consumers, so there is no separate fetchComments call.
 */
const useCommentStore = create((set) => ({
  // Keyed by postId: { [postId]: Comment[] }
  byPost: {},
  loading: false,
  error: null,

  // ── seed ──────────────────────────────────────────────────────────────────
  // Called by the Post page after fetchPost resolves, to populate the map.
  seed: (postId, comments) =>
    set((s) => ({ byPost: { ...s.byPost, [postId]: comments ?? [] } })),

  // ── createComment ─────────────────────────────────────────────────────────
  createComment: async (postId, content) => {
    set({ loading: true, error: null })
    try {
      const { data } = await api.post('/comment/create', {
        post_id: postId,
        content,
      })
      set((s) => ({
        loading: false,
        byPost: {
          ...s.byPost,
          [postId]: [data, ...(s.byPost[postId] ?? [])],
        },
      }))
      return data
    } catch (err) {
      set({ loading: false, error: err.message })
      throw err
    }
  },

  // ── voteComment ───────────────────────────────────────────────────────────
  // voteState: 1 = up, -1 = down, 0 = remove
  voteComment: async (postId, commentId, voteState) => {
    try {
      await api.post('/comment/vote', {
        comment_id: commentId,
        vote_state: voteState,
      })
      // Optimistic update
      set((s) => ({
        byPost: {
          ...s.byPost,
          [postId]: (s.byPost[postId] ?? []).map((c) =>
            c.id === commentId ? { ...c, score: c.score + voteState } : c,
          ),
        },
      }))
    } catch (err) {
      throw err
    }
  },

  clearError: () => set({ error: null }),
}))

export default useCommentStore
