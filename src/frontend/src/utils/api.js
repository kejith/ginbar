import axios from 'axios'

/**
 * Thin axios instance.
 * - baseURL '/api' so every call is relative, works with the Vite proxy in
 *   dev and with nginx in production.
 * - withCredentials sends the session cookie on cross-origin requests.
 * - Interceptor normalises error messages so stores only have to catch a
 *   single shape: { message: string, status: number }.
 */
const api = axios.create({
  baseURL: '/api',
  withCredentials: true,
  headers: { 'Content-Type': 'application/json' },
})

api.interceptors.response.use(
  (res) => res,
  (err) => {
    const status = err.response?.status ?? 0
    const message =
      err.response?.data?.error ??
      err.response?.data?.message ??
      err.message ??
      'Unknown error'
    return Promise.reject({ message, status })
  },
)

export default api
