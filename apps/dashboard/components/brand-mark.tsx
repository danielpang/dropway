import * as React from "react";

import { cn } from "@/lib/utils";

/**
 * The Dropway Prism mark: the glyph knocked out in white on the brand-indigo
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
      <g transform="translate(17 17) scale(0.66)">
        <path
          fillRule="evenodd"
          fill="#ffffff"
          d="M50 7 L85 55 L62 92 L38 92 L15 55 Z M50 7 L34 51 L46 58 Z"
        />
      </g>
    </svg>
  );
}
