"use client";

import * as React from "react";
import { Check, Copy } from "lucide-react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

/** A copy-to-clipboard button with a transient check state. */
export function CopyButton({
  value,
  label,
  full,
}: {
  value: string;
  label: string;
  full?: boolean;
}) {
  const [copied, setCopied] = React.useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard blocked (permissions / insecure context) — the field is
      // selectable, so the user can still copy manually. Nothing to surface.
    }
  }

  if (full) {
    return (
      <Button type="button" variant="outline" size="sm" onClick={copy} className="w-full">
        {copied ? <Check aria-hidden /> : <Copy aria-hidden />}
        {copied ? "Copied" : label}
      </Button>
    );
  }

  return (
    <Button
      type="button"
      variant="outline"
      size="icon"
      onClick={copy}
      aria-label={label}
    >
      {copied ? (
        <Check className={cn("size-4", "text-emerald-500")} aria-hidden />
      ) : (
        <Copy className="size-4" aria-hidden />
      )}
    </Button>
  );
}
