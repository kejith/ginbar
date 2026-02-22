/**
 * SectionHeader — labelled divider used before content sections.
 *
 * Props:
 *   title     string     section heading text
 *   children  ReactNode  optional right-side slot (action button, link, etc.)
 *   className string     extra classes on the wrapper div
 */
export default function SectionHeader({ title, children, className = "" }) {
  return (
    <div
      className={`flex items-center justify-between border-b border-(--color-border) pb-2 ${className}`}
    >
      <h2 className="text-xs font-semibold uppercase tracking-widest text-(--color-muted)">
        {title}
      </h2>
      {children && <div>{children}</div>}
    </div>
  );
}
