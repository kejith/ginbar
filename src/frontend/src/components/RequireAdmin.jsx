import { Navigate } from "react-router-dom";
import useAuthStore from "../stores/authStore.js";
import { isAdmin } from "../utils/roles.js";

/**
 * Wraps routes that require admin-level access (level >= 10).
 * Redirects non-admins to / after auth hydration completes.
 */
export default function RequireAdmin({ children }) {
  const user = useAuthStore((s) => s.user);

  if (user === null) {
    return <div className="p-4 text-zinc-500">loading…</div>;
  }
  if (!isAdmin(user)) {
    return <Navigate to="/" replace />;
  }
  return children;
}
