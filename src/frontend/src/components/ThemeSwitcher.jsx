import { useRef, useState, useEffect } from "react";
import useThemeStore, { THEMES } from "../stores/themeStore.js";

export default function ThemeSwitcher() {
  const { theme, setTheme } = useThemeStore();
  const [open, setOpen] = useState(false);
  const ref = useRef(null);

  // Close when clicking outside
  useEffect(() => {
    if (!open) return;
    function handleClick(e) {
      if (ref.current && !ref.current.contains(e.target)) setOpen(false);
    }
    document.addEventListener("mousedown", handleClick);
    return () => document.removeEventListener("mousedown", handleClick);
  }, [open]);

  const current = THEMES.find((t) => t.id === theme) ?? THEMES[0];

  return (
    <div ref={ref} className="relative shrink-0">
      {/* Trigger — shows current accent dot */}
      <button
        onClick={() => setOpen((v) => !v)}
        title="Switch theme"
        className="flex h-7 w-7 cursor-pointer items-center justify-center rounded-sm border border-(--color-border) bg-(--color-surface) transition-colors hover:border-(--color-accent)"
        aria-label="Theme switcher"
      >
        <span
          className="block h-3.5 w-3.5 rounded-full"
          style={{ background: current.accent }}
        />
      </button>

      {/* Dropdown */}
      {open && (
        <div className="absolute right-0 top-9 z-[200] w-48 overflow-hidden rounded-[var(--radius-sm)] border border-(--color-border) bg-(--color-surface) shadow-xl">
          {THEMES.map((t) => (
            <button
              key={t.id}
              onClick={() => {
                setTheme(t.id);
                setOpen(false);
              }}
              className={`flex w-full cursor-pointer items-center gap-2.5 px-3 py-2 text-left text-xs transition-colors hover:bg-(--color-bg) ${
                theme === t.id ? "text-(--color-text)" : "text-(--color-muted)"
              }`}
            >
              <span
                className="block h-3 w-3 shrink-0 rounded-full"
                style={{ background: t.accent }}
              />
              <span className="font-medium">{t.label}</span>
              {theme === t.id && (
                <span className="ml-auto text-[10px] text-(--color-accent)">
                  ✓
                </span>
              )}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
