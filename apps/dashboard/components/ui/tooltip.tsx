"use client";

import * as React from "react";

import { cn } from "@/lib/utils";

/**
 * Minimal, dependency-free tooltip (the dashboard has no @radix-ui/react-tooltip
 * in its dependency set). Shows `label` on hover AND keyboard focus of the
 * trigger, and exposes it to assistive tech via aria-describedby, so a disabled
 * control's reason is announced, not just shown on hover.
 *
 * Note: a natively `disabled` button does not fire pointer/focus events, so wrap
 * such triggers in a focusable element (or pass `disabled` styling without the
 * native attribute) when you need the tooltip to appear over them. The Tooltip
 * itself wraps the trigger in a focusable span as a fallback.
 *
 * `align` controls which edge of the bubble is pinned to the trigger. There is no
 * positioning engine here, so a centered bubble on a trigger near the viewport
 * edge overflows the page: it is absolutely positioned, but it still widens the
 * document, and `html { overflow-x: clip }` then makes that strip unreachable at
 * any zoom level. Use align="end" for triggers on the right (the app header),
 * "start" for triggers on the left. The width is also clamped to the viewport so
 * a long label can never push past the edge on a phone.
 */
export function Tooltip({
  label,
  children,
  side = "bottom",
  align = "center",
  className,
}: {
  label: string;
  children: React.ReactNode;
  side?: "top" | "bottom";
  align?: "center" | "start" | "end";
  className?: string;
}) {
  const id = React.useId();

  return (
    <span className={cn("group/tooltip relative inline-flex", className)}>
      <span
        tabIndex={0}
        aria-describedby={id}
        className="inline-flex rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
      >
        {children}
      </span>
      <span
        id={id}
        role="tooltip"
        className={cn(
          "pointer-events-none absolute z-50 w-max rounded-md border border-border bg-popover px-2.5 py-1.5 text-xs text-popover-foreground shadow-md",
          "max-w-[min(20rem,calc(100vw-1.5rem))]",
          "opacity-0 transition-opacity duration-150",
          "group-hover/tooltip:opacity-100 group-focus-within/tooltip:opacity-100",
          side === "bottom" ? "top-full mt-2" : "bottom-full mb-2",
          align === "center" && "left-1/2 -translate-x-1/2",
          align === "start" && "left-0",
          align === "end" && "right-0",
        )}
      >
        {label}
      </span>
    </span>
  );
}
