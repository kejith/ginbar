import { create } from 'zustand'
import api from '../utils/api.js'

/**
 * Auth store — wraps /api/check/me, /api/user/login, /api/user/logout,
 * /api/user/create.
 *
 * Shape of `user` when logged in: { id, name, level }
 */
const useAuthStore = create((set) => ({
  // null = unknown (not yet hydrated), false = confirmed guest
  user: null,
  loading: false,
  error: null,

  // ── hydrate ──────────────────────────────────────────────────────────────
  // Call once at app mount to restore session from cookie.
  hydrate: async () => {
    set({ loading: true, error: null })
    try {
      const { data } = await api.get('/check/me')
      set({ user: data, loading: false })
    } catch (err) {
      // 401 means guest — not an error worth surfacing
      set({ user: false, loading: false, error: err.status !== 401 ? err.message : null })
    }
  },

  // ── login ─────────────────────────────────────────────────────────────────
  login: async (name, password) => {
    set({ loading: true, error: null })
    try {
      const { data } = await api.post('/user/login', { name, password })
      set({ user: data, loading: false })
      return data
    } catch (err) {
      set({ loading: false, error: err.message })
      throw err
    }
  },

  // ── logout ────────────────────────────────────────────────────────────────
  logout: async () => {
    set({ loading: true, error: null })
    try {
      await api.post('/user/logout')
      set({ user: false, loading: false })
    } catch (err) {
      set({ loading: false, error: err.message })
      throw err
    }
  },

  // ── register ──────────────────────────────────────────────────────────────
  register: async (name, email, password) => {
    set({ loading: true, error: null })
    try {
      await api.post('/user/create', { name, email, password })
      // Log in immediately after registration
      const { data } = await api.post('/user/login', { name, password })
      set({ user: data, loading: false })
      return data
    } catch (err) {
      set({ loading: false, error: err.message })
      throw err
    }
  },

  clearError: () => set({ error: null }),
}))

export default useAuthStore
