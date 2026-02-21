import { create } from "zustand";
import api from "../utils/api.js";

/**
 * Invite store — wraps /api/invite endpoints.
 *
 * Endpoints:
 *   POST /api/invite           → create a new invitation token (auth required)
 *   GET  /api/invite           → list caller's invitations (auth required)
 *   GET  /api/invite/:token    → validate a token (public)
 */
const useInviteStore = create((set) => ({
  invites: [],
  loading: false,
  error: null,

  // ── create ─────────────────────────────────────────────────────────────────
  createInvite: async () => {
    set({ loading: true, error: null });
    try {
      const { data } = await api.post("/invite/");
      set((s) => ({
        invites: [data, ...s.invites],
        loading: false,
      }));
      return data.token;
    } catch (err) {
      set({ loading: false, error: err.message });
      throw err;
    }
  },

  // ── list ───────────────────────────────────────────────────────────────────
  fetchInvites: async () => {
    set({ loading: true, error: null });
    try {
      const { data } = await api.get("/invite/");
      set({ invites: data.data ?? [], loading: false });
    } catch (err) {
      set({ loading: false, error: err.message });
    }
  },

  // ── validate ───────────────────────────────────────────────────────────────
  // Returns { valid: bool, reason?: string }
  validateInvite: async (token) => {
    const { data } = await api.get(`/invite/${token}`);
    return data;
  },
}));

export default useInviteStore;
