import { useState } from "react";
import { useNavigate } from "react-router-dom";
import useAuthStore from "../stores/authStore.js";
import FormField, { inputCls } from "../components/FormField.jsx";

export default function Login() {
  const { login, loading, error, clearError } = useAuthStore();
  const navigate = useNavigate();
  const [form, setForm] = useState({ name: "", password: "" });

  function change(e) {
    clearError();
    setForm((f) => ({ ...f, [e.target.name]: e.target.value }));
  }

  async function submit(e) {
    e.preventDefault();
    try {
      await login(form.name, form.password);
      navigate("/");
    } catch {
      // error is already in the store
    }
  }

  return (
    <main className="mx-auto mt-16 max-w-sm px-4">
      <h1 className="mb-6 text-xl font-bold text-(--color-text)">sign in</h1>
      <form onSubmit={submit} className="flex flex-col gap-3">
        <FormField label="username">
          <input
            name="name"
            value={form.name}
            onChange={change}
            autoComplete="username"
            required
            className={inputCls}
          />
        </FormField>
        <FormField label="password">
          <input
            name="password"
            type="password"
            value={form.password}
            onChange={change}
            autoComplete="current-password"
            required
            className={inputCls}
          />
        </FormField>
        {error && <p className="text-xs text-(--color-danger)">{error}</p>}
        <button
          type="submit"
          disabled={loading}
          className="mt-2 rounded bg-(--color-accent) py-2 text-sm font-semibold text-white disabled:opacity-50"
        >
          {loading ? "…" : "sign in"}
        </button>
      </form>
      <p className="mt-4 text-center text-sm text-(--color-muted)">
        no account? ask a member for an invite link.
      </p>
    </main>
  );
}
