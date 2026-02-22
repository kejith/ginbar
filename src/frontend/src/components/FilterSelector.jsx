import usePostStore from "../stores/postStore.js";
import useAuthStore from "../stores/authStore.js";
import { feedFilterOptions } from "../utils/roles.js";

/**
 * FilterSelector — tab bar that lets members choose which content category
 * to display in the feed.
 *
 * Guests get no selector (they always see SFW content).
 * Normal members see:   SFW | NSFW
 * Secret / admin see:   All | SFW | NSFP | NSFW | Secret
 *
 * Changing the filter resets the post list via postStore.setFilter().
 */
export default function FilterSelector() {
  const user = useAuthStore((s) => s.user);
  const activeFilter = usePostStore((s) => s.activeFilter);
  const setFilter = usePostStore((s) => s.setFilter);

  const options = feedFilterOptions(user);
  if (options.length === 0) return null; // guests — don't render

  return (
    <div className="flex items-center gap-1 px-3 py-2 border-b border-(--color-border) bg-(--color-surface)">
      {options.map((opt) => {
        const active = opt.value === activeFilter;
        return (
          <button
            key={opt.value}
            onClick={() => {
              if (!active) setFilter(opt.value);
            }}
            className={[
              "rounded-md px-3 py-1 text-xs font-medium transition-colors",
              active
                ? "bg-(--color-accent) text-(--color-accent-text)"
                : "text-(--color-muted) hover:text-(--color-text) hover:bg-(--color-bg)",
            ].join(" ")}
          >
            {opt.label}
          </button>
        );
      })}
    </div>
  );
}
