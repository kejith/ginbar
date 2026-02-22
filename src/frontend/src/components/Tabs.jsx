/**
 * Tabs — underline-style tab bar.
 *
 * Props:
 *   tabs      [{ id: string, label: ReactNode }]
 *   active    string  — active tab id
 *   onChange  fn(id)  — called when a tab is clicked
 *   className string  — extra classes on the wrapper div
 */
export default function Tabs({ tabs, active, onChange, className = "" }) {
  return (
    <div className={`flex gap-1 border-b border-(--color-border) ${className}`}>
      {tabs.map((t) => (
        <button
          key={t.id}
          type="button"
          onClick={() => onChange(t.id)}
          className={`-mb-px border-b-2 px-3 py-1.5 text-sm font-medium transition-colors ${
            active === t.id
              ? "border-(--color-accent) text-(--color-accent)"
              : "border-transparent text-(--color-muted) hover:text-(--color-text)"
          }`}
        >
          {t.label}
        </button>
      ))}
    </div>
  );
}
