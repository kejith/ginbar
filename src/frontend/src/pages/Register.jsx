import { useState, useEffect } from "react";
import { useNavigate, useSearchParams, Link } from "react-router-dom";
import useAuthStore from "../stores/authStore.js";
import useInviteStore from "../stores/inviteStore.js";

/**
 * /register?invite=<token>
 *
 * Validates the invite token on mount.  If valid, shows the registration form.
 * Registration is impossible without a valid, unused invite token.
 */
export default function Register() {
  const [searchParams] = useSearchParams();
  const token = searchParams.get("invite") ?? "";

  const navigate = useNavigate();
  const {
    register,
    loading: authLoading,
    error: authError,
    clearError,
  } = useAuthStore();
  const validateInvite = useInviteStore((s) => s.validateInvite);

  const [tokenStatus, setTokenStatus] = useState("checking"); // "checking" | "valid" | "invalid"
  const [tokenReason, setTokenReason] = useState("");
  const [form, setForm] = useState({ name: "", email: "", password: "" });

  // Validate token on mount / when token changes.
  useEffect(() => {
    if (!token) {
      setTokenStatus("invalid");
      setTokenReason(
        "No invitation token provided. Ask an existing member for an invite link.",
      );
      return;
    }

    setTokenStatus("checking");
    validateInvite(token)
      .then((result) => {
        if (result.valid) {
          setTokenStatus("valid");
        } else {
          setTokenStatus("invalid");
          setTokenReason(
            result.reason === "already used"
              ? "This invitation has already been used."
              : "This invitation link is invalid.",
          );
        }
      })
      .catch(() => {
        setTokenStatus("invalid");
        setTokenReason("Could not validate the invitation. Please try again.");
      });
  }, [token]);

  function change(e) {
    clearError();
    setForm((f) => ({ ...f, [e.target.name]: e.target.value }));
  }

  async function submit(e) {
    e.preventDefault();
    try {
      await register(form.name, form.email, form.password, token);
      navigate("/");
    } catch {
      // error shown via authError
    }
  }

  // ── loading state ──────────────────────────────────────────────────────────
  if (tokenStatus === "checking") {
    return (
      <main className="mx-auto mt-16 max-w-sm px-4 text-center text-(--color-muted)">
        checking invitation…
      </main>
    );
  }

  // ── invalid token ──────────────────────────────────────────────────────────
  if (tokenStatus === "invalid") {
    return (
      <main className="mx-auto mt-16 max-w-sm px-4">
        <h1 className="mb-4 text-xl font-bold text-(--color-text)">
          invitation invalid
        </h1>
        <p className="mb-6 text-sm text-(--color-muted)">{tokenReason}</p>
        <Link
          to="/login"
          className="text-sm underline text-(--color-muted) hover:text-(--color-text)"
        >
          sign in instead
        </Link>
      </main>
    );
  }

  // ── valid token — show registration form ───────────────────────────────────
  return (
    <main className="mx-auto mt-16 max-w-sm px-4">
      <h1 className="mb-2 text-xl font-bold text-(--color-text)">
        create account
      </h1>
      <p className="mb-6 text-xs text-(--color-muted)">
        You&apos;ve been invited. Fill in your details below.
      </p>

      <form onSubmit={submit} className="flex flex-col gap-3">
        <label className="flex flex-col gap-1 text-sm">
          <span className="text-(--color-muted)">username</span>
          <input
            name="name"
            value={form.name}
            onChange={change}
            autoComplete="username"
            minLength={4}
            required
            className="rounded bg-(--color-bg) px-3 py-2 text-(--color-text) ring-1 ring-(--color-border) focus:outline-none focus:ring-(--color-accent)"
          />
        </label>

        <label className="flex flex-col gap-1 text-sm">
          <span className="text-(--color-muted)">email</span>
          <input
            name="email"
            type="email"
            value={form.email}
            onChange={change}
            autoComplete="email"
            required
            className="rounded bg-(--color-bg) px-3 py-2 text-(--color-text) ring-1 ring-(--color-border) focus:outline-none focus:ring-(--color-accent)"
          />
        </label>

        <label className="flex flex-col gap-1 text-sm">
          <span className="text-(--color-muted)">password</span>
          <input
            name="password"
            type="password"
            value={form.password}
            onChange={change}
            autoComplete="new-password"
            minLength={6}
            required
            className="rounded bg-(--color-bg) px-3 py-2 text-(--color-text) ring-1 ring-(--color-border) focus:outline-none focus:ring-(--color-accent)"
          />
        </label>

        {authError && <p className="text-xs text-red-400">{authError}</p>}

        <button
          type="submit"
          disabled={authLoading}
          className="mt-2 rounded bg-(--color-accent) py-2 text-sm font-semibold text-white disabled:opacity-50"
        >
          {authLoading ? "…" : "create account"}
        </button>
      </form>

      <p className="mt-4 text-center text-sm text-(--color-muted)">
        already have an account?{" "}
        <Link to="/login" className="underline hover:text-(--color-text)">
          sign in
        </Link>
      </p>
    </main>
  );
}
