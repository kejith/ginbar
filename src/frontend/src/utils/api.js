import axios from "axios";

/**
 * Thin axios instance.
 * - baseURL '/api' so every call is relative, works with the Vite proxy in
 *   dev and with nginx in production.
 * - withCredentials sends the session cookie on cross-origin requests.
 * - Interceptor normalises error messages so stores only have to catch a
 *   single shape: { message: string, status: number }.
 */
const api = axios.create({
  baseURL: "/api",
  withCredentials: true,
  headers: { "Content-Type": "application/json" },
});

api.interceptors.response.use(
  (res) => res,
  (err) => {
    const status = err.response?.status ?? 0;
    const message =
      err.response?.data?.error ??
      err.response?.data?.message ??
      err.message ??
      "Unknown error";
    return Promise.reject({ message, status });
  },
);

export default api;

/**
 * POST to an SSE endpoint and stream events back.
 *
 * @param {string} path       - path relative to /api (e.g. '/admin/posts/regenerate-images')
 * @param {object} body       - JSON body to POST
 * @param {(event: object) => void} onEvent - called for every parsed SSE data frame
 * @returns {Promise<object>} - resolves with the last received event object
 */
export async function ssePost(path, body, onEvent) {
  const res = await fetch(`/api${path}`, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body ?? {}),
  });

  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw {
      message: json.error ?? json.message ?? `HTTP ${res.status}`,
      status: res.status,
    };
  }

  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let last = null;

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split("\n");
    buffer = lines.pop(); // keep any incomplete trailing line
    for (const line of lines) {
      if (line.startsWith("data:")) {
        try {
          const event = JSON.parse(line.slice(5).trim());
          last = event;
          onEvent(event);
        } catch {
          /* ignore malformed frames */
        }
      }
    }
  }

  return last;
}
