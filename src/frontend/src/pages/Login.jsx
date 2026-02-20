import { useState } from "react";
import { useNavigate } from "react-router-dom";
import useAuthStore from "../stores/authStore.js";

export default function Login() {
  const { login, register, loading, error, clearError } = useAuthStore();
  const navigate = useNavigate();
  const [mode, setMode] = useState("login");
  const [form, setForm] = useState({ name: "", email: "", password: "" });

  function change(e) {
    clearError();
    setForm((f) => ({ ...f, [e.target.name]: e.target.value }));
  }

  async function submit(e) {
    e.preventDefault();
    try {
      if (mode === "login") {
        await login(form.name, form.password);
      } else {
        await register(form.name, form.email, form.password);
      }
      navigate("/");
    } catch {
      // error is already in the store
    }
  }

  return (
    <main className="mx-auto mt-16 max-w-sm px-4">
      <h1 className="mb-6 text-xl font-bold text-(--color-text)">
        {mode === "login" ? "sign in" : "create account"}
      </h1>
      <form onSubmit={submit} className="flex flex-col gap-3">
        <label className="flex flex-col gap-1 text-sm">
          <span className="text-(--color-muted)">username</span>
          <input
            name="name"
            value={form.name}
            onChange={change}
            autoComplete="username"
            required
            className="rounded bg-(--color-bg) px-3 py-2 text-(--color-text) ring-1 ring-(--color-border) focus:outline-none focus:ring-(--color-accent)"
          />
        </label>
        {mode === "register" && (
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
        )}
        <label className="flex flex-col gap-1 text-sm">
          <span className="text-(--color-muted)">password</span>
          <input
            name="password"
            type="password"
            value={form.password}
            onChange={change}
            autoComplete={mode === "login" ? "current-password" : "new-password"}
            required
            className="rounded bg-(--color-bg) px-3 py-2 text-(--color-text) ring-1 ring-(--color-border) focus:outline-none focus:ring-(--color-accent)"
          />
        </label>
        {error && <p className="text-xs text-red-400">{error}</p>}
        <button
          type="submit"
          disabled={loading}
          className="mt-2 rounded bg-(--color-accent) py-2 text-sm font-semibold text-white disabled:opacity-50"
        >
          {loading ? "…" : mode === "login" ? "sign in" : "create account"}
        </button>
      </form>
      <p className="mt-4 text-center text-sm text-(--color-muted)">
        {mode === "login" ? (
          <>
            no account?{" "}
            <button onClick={() => { clearError(); setMode("register"); }} className="underline hover:text-(--color-text)">
              register
            </button>
          </>
        ) : (
          <>
            have an account?{" "}
            <button onClick={() => { clearError(); setMode("login"); }} className="underline hover:text-(--color-text)">
              sign in
            </button>
          </>
        )}
      </p>
    </main>
  );
}
