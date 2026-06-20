import { cn } from "@/lib/utils";

/**
 * A placeholder block that gently pulses while real content loads. Used by
 * route-level `loading.tsx` files so a page's structure paints instantly on
 * navigation and only the data-dependent content is deferred. Token-driven
 * (`bg-muted`) so it reads correctly in both themes; the pulse respects
 * `prefers-reduced-motion` (globals.css disables animations for those users).
 */
export function Skeleton({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn("animate-pulse rounded-md bg-muted", className)}
      {...props}
    />
  );
}
