import { Navigate } from "react-router-dom";
import useAuthStore from "../stores/authStore.js";

/**
 * Wraps routes that require a logged-in user.
 * Redirects to /login when the session is confirmed as guest (user === false).
 * Shows a loading state while auth is still hydrating (user === null).
 */
export default function RequireAuth({ children }) {
  const user = useAuthStore((s) => s.user);

  if (user === null) {
    // Still hydrating — render nothing until we know auth state.
    return <div className="p-4 text-(--color-muted)">loading…</div>;
  }
  if (user === false) {
    return <Navigate to="/login" replace />;
  }
  return children;
}
