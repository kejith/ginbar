/**
 * FormField — label + input/textarea wrapper with optional error message.
 *
 * Use `inputCls` on any `<input>`, `<textarea>`, or `<select>` to get the
 * consistent ring-focus style used throughout the app.
 *
 * Props:
 *   label     string     label text (optional — omit for unlabelled inputs)
 *   error     string     validation / server error shown below the field
 *   children  ReactNode  the actual `<input>` / `<textarea>` / `<select>`
 */
export const inputCls =
  "w-full rounded bg-(--color-bg) px-3 py-2 text-sm text-(--color-text) " +
  "ring-1 ring-(--color-border) focus:outline-none focus:ring-(--color-accent) " +
  "placeholder:text-(--color-muted) disabled:opacity-50";

export default function FormField({ label, error, children }) {
  return (
    <label className="flex flex-col gap-1 text-sm">
      {label && <span className="text-(--color-muted)">{label}</span>}
      {children}
      {error && <p className="mt-0.5 text-xs text-(--color-danger)">{error}</p>}
    </label>
  );
}
