import { useRef, useState, useEffect } from "react";
import useThemeStore, { THEMES } from "../stores/themeStore.js";
import useAuthStore from "../stores/authStore.js";
import usePostStore from "../stores/postStore.js";
import { feedFilterOptions } from "../utils/roles.js";

export default function SettingsMenu() {
  const { theme, setTheme } = useThemeStore();
  const [open, setOpen] = useState(false);
  const ref = useRef(null);
  const user = useAuthStore((s) => s.user);
  const activeFilters = usePostStore((s) => s.activeFilters);
  const setFilters = usePostStore((s) => s.setFilters);

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
  const filterOptions = feedFilterOptions(user);

  function toggle(value) {
    if (activeFilters.includes(value)) {
      setFilters(activeFilters.filter((f) => f !== value));
    } else {
      setFilters([...activeFilters, value]);
    }
  }

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
        <div className="absolute right-0 top-9 z-[200] w-52 overflow-hidden rounded-[var(--radius-sm)] border border-(--color-border) bg-(--color-surface) shadow-xl">
          {/* Feed filters section */}
          {filterOptions.length > 0 && (
            <>
              <div className="px-3 pt-2 pb-1 text-[10px] font-semibold uppercase tracking-wider text-(--color-muted)">
                Feed
              </div>
              <div className="flex flex-wrap gap-1 px-3 pb-2">
                {filterOptions.map((opt) => {
                  const active = activeFilters.includes(opt.value);
                  return (
                    <button
                      key={opt.value}
                      onClick={() => toggle(opt.value)}
                      className={`h-6 rounded-sm px-2 text-xs ring-1 cursor-pointer transition-colors ${
                        active
                          ? "bg-(--color-accent) text-(--color-bg) ring-(--color-accent)"
                          : "bg-(--color-bg) text-(--color-muted) ring-(--color-border) hover:ring-(--color-accent) hover:text-(--color-text)"
                      }`}
                    >
                      {opt.label}
                    </button>
                  );
                })}
              </div>
              <div className="mx-2 mb-1 border-t border-(--color-border)" />
            </>
          )}

          {/* Theme section */}
          <div className="px-3 pt-1 pb-1 text-[10px] font-semibold uppercase tracking-wider text-(--color-muted)">
            Theme
          </div>
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
