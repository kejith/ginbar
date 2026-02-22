import usePostStore from "../stores/postStore.js";
import useAuthStore from "../stores/authStore.js";
import { feedFilterOptions } from "../utils/roles.js";

/**
 * FilterSelector — multi-toggle that lets members choose which content
 * categories appear in the feed.  Intended for use inside the navbar.
 *
 * Guests:          hidden (always SFW)
 * Normal members:  SFW | NSFP | NSFW
 * Secret / admin:  SFW | NSFP | NSFW | Secret
 *
 * SFW and NSFP are on by default; NSFW and Secret start off.
 */
export default function FilterSelector() {
  const user = useAuthStore((s) => s.user);
  const activeFilters = usePostStore((s) => s.activeFilters);
  const setFilters = usePostStore((s) => s.setFilters);

  const options = feedFilterOptions(user);
  if (options.length === 0) return null; // guests — don't render

  function toggle(value) {
    if (activeFilters.includes(value)) {
      setFilters(activeFilters.filter((f) => f !== value));
    } else {
      setFilters([...activeFilters, value]);
    }
  }

  return (
    <div className="flex items-center gap-1">
      {options.map((opt) => {
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
  );
}
