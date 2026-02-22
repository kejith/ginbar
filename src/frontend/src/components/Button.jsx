/**
 * Button — unified button/link-button with semantic variants.
 *
 * Props:
 *   variant   "primary" | "danger" | "admin" | "ghost"  (default "primary")
 *   size      "xs" | "sm" | "md"                        (default "sm")
 *   as        "button" | "a"                            (default "button")
 *   className  additional classes merged in
 *   ...rest    forwarded to the underlying element
 */
const BASE =
  "inline-flex items-center justify-center font-semibold transition-opacity disabled:opacity-50 rounded-[var(--radius-sm)]";

const VARIANT = {
  primary: "bg-(--color-accent) text-(--color-accent-text) hover:opacity-90",
  danger: "bg-(--color-danger) text-white hover:opacity-90",
  admin: "bg-(--color-admin) text-white hover:opacity-90",
  ghost: "text-(--color-muted) hover:text-(--color-text)",
};

const SIZE = {
  xs: "px-2 py-0.5 text-xs",
  sm: "px-3 py-1 text-xs",
  md: "px-4 py-1.5 text-sm",
  lg: "px-4 py-2 text-sm",
};

export default function Button({
  variant = "primary",
  size = "sm",
  as: Tag = "button",
  className = "",
  children,
  ...rest
}) {
  return (
    <Tag
      className={`${BASE} ${VARIANT[variant]} ${SIZE[size]} ${className}`}
      {...rest}
    >
      {children}
    </Tag>
  );
}
