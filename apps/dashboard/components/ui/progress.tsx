import * as React from "react";

import { cn } from "@/lib/utils";

/**
 * Minimal determinate progress bar. `value` is 0 to 100; out-of-range is clamped.
 * Token-driven (bg-secondary track, bg-primary fill) so it matches both themes.
 */
export function Progress({
  value = 0,
  className,
}: {
  value?: number;
  className?: string;
}) {
  const pct = Math.max(0, Math.min(100, value));
  return (
    <div
      role="progressbar"
      aria-valuenow={Math.round(pct)}
      aria-valuemin={0}
      aria-valuemax={100}
      className={cn(
        "h-2 w-full overflow-hidden rounded-full bg-secondary",
        className,
      )}
    >
      <div
        className="h-full rounded-full bg-primary transition-[width] duration-300 ease-out"
        style={{ width: `${pct}%` }}
      />
    </div>
  );
}
