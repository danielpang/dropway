import * as React from "react";

import { cn } from "@/lib/utils";

/**
 * The Dropway mark: a rounded "D" whose counter is a droplet, knocked out in white on the brand-indigo
 * tile. Brand colors are intentionally hard-coded (not theme tokens) so the logo
 * reads as the same mark in light and dark, matching the favicon. The app's own
 * UI stays monochrome; this is the one deliberate spot of brand color.
 */
export function BrandMark({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 100 100"
      className={cn("size-6 shrink-0", className)}
      role="img"
      aria-hidden
      focusable="false"
    >
      <rect width="100" height="100" rx="18" fill="#5647e1" />
      <path
        fillRule="evenodd"
        fill="#ffffff"
        d="M30 18 H44 A32 32 0 0 1 44 82 H30 A6 6 0 0 1 24 76 V24 A6 6 0 0 1 30 18 Z M48 30 C48 30 35 46 35 58 A13 13 0 0 0 61 58 C61 46 48 30 48 30 Z"
      />
    </svg>
  );
}
