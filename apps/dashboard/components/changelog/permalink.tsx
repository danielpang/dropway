"use client";

import * as React from "react";
import { Check, Link2 } from "lucide-react";

import { cn } from "@/lib/utils";

/**
 * A heading with a Cursor-style permalink control. The link icon sits to the
 * left of the text and fades in on hover/focus of the whole `group` row; clicking
 * it copies the absolute URL to this anchor AND updates the address bar hash (so
 * a plain click still deep-links, and right-click "copy link" works too).
 *
 * The wrapping element owns the anchor `id` and `scroll-mt` so `#id` navigation
 * lands with the heading clear of the sticky app header.
 */
export function Permalink({
  id,
  as: Tag = "h2",
  children,
  className,
  headingClassName,
}: {
  id: string;
  as?: "h2" | "h3";
  children: React.ReactNode;
  /** Classes for the wrapper (the scroll-margin anchor + hover group). */
  className?: string;
  /** Classes for the heading text itself. */
  headingClassName?: string;
}) {
  const [copied, setCopied] = React.useState(false);
  const timer = React.useRef<ReturnType<typeof setTimeout> | null>(null);

  React.useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current);
    },
    [],
  );

  const href = `#${id}`;

  async function onCopy() {
    // Let the native anchor set the hash (deep link); we additionally copy the
    // full URL so the user can paste it elsewhere. We don't preventDefault, so
    // the hash still updates even if the clipboard write is denied.
    const url =
      typeof window !== "undefined"
        ? `${window.location.origin}${window.location.pathname}${href}`
        : href;
    try {
      await navigator.clipboard?.writeText(url);
      setCopied(true);
      if (timer.current) clearTimeout(timer.current);
      timer.current = setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard denied (permissions / insecure context): the hash still
      // updates from the anchor, so deep-linking works regardless.
    }
  }

  return (
    <div id={id} className={cn("group scroll-mt-24", className)}>
      <Tag className={cn("flex items-center gap-2", headingClassName)}>
        <a
          href={href}
          onClick={onCopy}
          aria-label={copied ? "Link copied" : "Copy link to this section"}
          className={cn(
            "-ml-6 hidden size-5 shrink-0 items-center justify-center rounded text-muted-foreground",
            "opacity-0 transition-opacity hover:text-foreground focus-visible:opacity-100 group-hover:opacity-100 sm:inline-flex",
            "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background",
          )}
        >
          {copied ? (
            <Check className="size-4" aria-hidden />
          ) : (
            <Link2 className="size-4" aria-hidden />
          )}
        </a>
        <span>{children}</span>
      </Tag>
    </div>
  );
}
