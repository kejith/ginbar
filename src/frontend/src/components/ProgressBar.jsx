/**
 * ProgressBar — a simple accessible progress indicator.
 *
 * Props:
 *   value     number   0-100
 *   status    "active" | "success" | "error" | "pulse"
 *             "active"  → accent colour, determinate fill
 *             "success" → success colour (green)
 *             "error"   → danger colour (red)
 *             "pulse"   → full-width pulsing bar (indeterminate)
 *   height    "sm" | "md"   bar height  (default "sm")
 */
const STATUS_CLS = {
  active: "bg-(--color-accent)",
  success: "bg-(--color-success)",
  error: "bg-(--color-danger)",
  pulse: "bg-(--color-accent) w-full animate-pulse opacity-60",
};

const HEIGHT_CLS = {
  sm: "h-1.5",
  md: "h-2",
};

export default function ProgressBar({
  value = 0,
  status = "active",
  height = "sm",
}) {
  const isPulse = status === "pulse";
  return (
    <div
      className={`w-full overflow-hidden rounded-full bg-(--color-border) ${HEIGHT_CLS[height]}`}
    >
      <div
        className={`h-full rounded-full transition-all duration-300 ease-out ${STATUS_CLS[status]}`}
        style={isPulse ? undefined : { width: `${value}%` }}
      />
    </div>
  );
}
