import * as React from "react";

import { cn } from "@/lib/utils";

/**
 * The Dropway mark: three stacked pages (deploy versions) with a folded corner
 * on the newest one, drawn without a background tile. It is brand indigo in the
 * light theme and white (with an indigo fold) in the dark theme, matching the
 * favicon. Brand colors are intentionally hard-coded (not theme tokens): the
 * app's own UI stays monochrome, and this is the one deliberate spot of brand
 * color.
 */
export function BrandMark({ className }: { className?: string }) {
  const card = "fill-[#5647e1] dark:fill-white";
  const fold = "fill-white dark:fill-[#5647e1]";
  return (
    <svg
      viewBox="0 0 100 100"
      className={cn("size-6 shrink-0", className)}
      role="img"
      aria-hidden
      focusable="false"
    >
      <rect className={card} opacity={0.3} x="32" y="4.5" width="60" height="69" rx="10.5" />
      <rect className={card} opacity={0.6} x="20" y="16.5" width="60" height="69" rx="10.5" />
      <path
        className={card}
        d="M18.5 28.5 H50 L68 46.5 V87 A10.5 10.5 0 0 1 57.5 97.5 H18.5 A10.5 10.5 0 0 1 8 87 V39 A10.5 10.5 0 0 1 18.5 28.5 Z"
      />
      <path className={fold} opacity={0.5} d="M50 28.5 V40.5 A6 6 0 0 0 56 46.5 H68 Z" />
    </svg>
  );
}
