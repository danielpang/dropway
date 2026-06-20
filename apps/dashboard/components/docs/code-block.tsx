"use client";

import * as React from "react";
import { Check, Copy } from "lucide-react";

import { cn } from "@/lib/utils";

/**
 * A copy-able code block for the in-app reference pages (mirrors the dropway-www
 * docs block so the experience matches, but lives here so signed-in users never
 * leave the dashboard). Renders the code as real, selectable text with an
 * optional label and a copy-to-clipboard button. Multi-line aware.
 */
export function CodeBlock({
  code,
  label,
  className,
}: {
  code: string;
  label?: string;
  className?: string;
}) {
  const [copied, setCopied] = React.useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(code);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard can be unavailable in insecure contexts; fail quietly.
    }
  }

  return (
    <div
      className={cn(
        "group relative overflow-hidden rounded-xl border border-border bg-card shadow-sm",
        className,
      )}
    >
      <div className="flex items-center justify-between border-b border-border bg-muted/50 px-4 py-2">
        <span className="select-none font-mono text-xs text-muted-foreground">
          {label ?? "shell"}
        </span>
        <button
          type="button"
          onClick={copy}
          aria-label={copied ? "Copied" : "Copy"}
          className="inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          {copied ? (
            <>
              <Check className="h-3.5 w-3.5 text-emerald-500" aria-hidden />
              Copied
            </>
          ) : (
            <>
              <Copy className="h-3.5 w-3.5" aria-hidden />
              Copy
            </>
          )}
        </button>
      </div>
      {/*
        Wrap long lines instead of scrolling them horizontally: on mobile a
        horizontally-scrolling code block reads as a page-level sideways scroll.
        whitespace-pre-wrap keeps real newlines; [overflow-wrap:anywhere] breaks
        otherwise-unbreakable tokens (long URLs) so nothing extends past the card.
      */}
      <pre className="whitespace-pre-wrap px-4 py-3.5 font-mono text-[0.82rem] leading-relaxed text-foreground [overflow-wrap:anywhere]">
        <code>{code}</code>
      </pre>
    </div>
  );
}
