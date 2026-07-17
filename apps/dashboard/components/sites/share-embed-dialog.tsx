"use client";

import * as React from "react";
import { Check, Code2, Copy, ExternalLink, Lock, Share2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogBody,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { buildEmbedSnippet, buildEmbedUrl } from "@/lib/embed";
import { cn } from "@/lib/utils";

/**
 * "Share → Embed" for a site. A trigger button opens a dialog that offers two ways
 * to share the deployed site:
 *   1. the raw link (copy-to-clipboard), and
 *   2. an <iframe> embed snippet (copy-to-clipboard) with adjustable width/height,
 *      so the site can be pasted into Notion / Linear / Confluence / any page.
 *
 * The embed points at the site's own content host with `?embed=1`, which the serving
 * layer renders framable and chrome-stripped. Access control is preserved end-to-end:
 * a PRIVATE site shows a "Sign in to view" placeholder inside the frame (never its
 * bytes), which we surface as a note here.
 *
 * The "Powered by Dropway" badge is shown by default. Pro+ orgs get a toggle to
 * remove it (adds `?badge=0`); the entitlement is re-checked server-side off the KV
 * route projection, so this toggle is UX only — a free org can't remove the badge.
 */
export function ShareEmbedDialog({
  liveUrl,
  title,
  isPrivate,
  badgeRemovable,
  disabled,
}: {
  /** The site's live URL (custom domain or `<slug>.dropwaycontent.com`). */
  liveUrl: string;
  /** A human title for the iframe (the site slug). */
  title: string;
  /** True when the site's access mode is not public (viewers see the placeholder). */
  isPrivate: boolean;
  /** True when the org may remove the attribution badge (Pro+). */
  badgeRemovable: boolean;
  /** Disable the trigger (e.g. the site isn't deployed yet). */
  disabled?: boolean;
}) {
  const [open, setOpen] = React.useState(false);
  const [width, setWidth] = React.useState("100%");
  const [height, setHeight] = React.useState("600");
  const [removeBadge, setRemoveBadge] = React.useState(false);

  const embedUrl = React.useMemo(
    () => buildEmbedUrl(liveUrl, badgeRemovable && removeBadge),
    [liveUrl, badgeRemovable, removeBadge],
  );
  const snippet = React.useMemo(
    () => buildEmbedSnippet(embedUrl, width, height, title),
    [embedUrl, width, height, title],
  );

  return (
    <>
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => setOpen(true)}
        disabled={disabled}
      >
        <Share2 aria-hidden />
        Share
      </Button>

      <Dialog open={open} onOpenChange={setOpen} label="Share this site" className="max-w-lg">
        <DialogHeader>
          <DialogTitle>Share this site</DialogTitle>
          <DialogDescription>
            Copy the link, or embed the site as an iframe in Notion, Linear, or any page.
          </DialogDescription>
        </DialogHeader>
        <DialogBody className="space-y-6">
          {/* Direct link */}
          <div className="space-y-2">
            <Label>Link</Label>
            <div className="flex items-center gap-2">
              <Input readOnly value={liveUrl} className="font-mono text-xs" />
              <CopyButton value={liveUrl} label="Copy link" />
              <Button asChild variant="outline" size="icon" aria-label="Open site">
                <a href={liveUrl} target="_blank" rel="noopener noreferrer">
                  <ExternalLink className="size-4" aria-hidden />
                </a>
              </Button>
            </div>
          </div>

          {/* Embed */}
          <div className="space-y-3">
            <div className="flex items-center gap-2">
              <Code2 className="size-4 text-muted-foreground" aria-hidden />
              <Label className="m-0">Embed</Label>
            </div>

            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label htmlFor="embed-width" className="text-xs text-muted-foreground">
                  Width
                </Label>
                <Input
                  id="embed-width"
                  value={width}
                  onChange={(e) => setWidth(e.target.value)}
                  placeholder="100%"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="embed-height" className="text-xs text-muted-foreground">
                  Height (px)
                </Label>
                <Input
                  id="embed-height"
                  inputMode="numeric"
                  value={height}
                  onChange={(e) => setHeight(e.target.value.replace(/[^0-9]/g, ""))}
                  placeholder="600"
                />
              </div>
            </div>

            {badgeRemovable ? (
              <label className="flex items-center justify-between gap-4 rounded-md border border-border px-3 py-2">
                <span className="text-sm">
                  Remove the &ldquo;Powered by Dropway&rdquo; badge
                </span>
                <Switch
                  checked={removeBadge}
                  onCheckedChange={setRemoveBadge}
                  aria-label="Remove the Powered by Dropway badge"
                />
              </label>
            ) : null}

            <div className="space-y-2">
              <textarea
                readOnly
                value={snippet}
                rows={3}
                onFocus={(e) => e.currentTarget.select()}
                className="w-full resize-none rounded-md border border-border bg-muted/50 px-3 py-2 font-mono text-xs text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              />
              <CopyButton value={snippet} label="Copy embed code" full />
            </div>

            {isPrivate ? (
              <p className="flex items-start gap-2 text-xs text-muted-foreground">
                <Lock className="mt-0.5 size-3.5 shrink-0" aria-hidden />
                <span>
                  This site is private. Visitors who aren&rsquo;t signed in will see a
                  &ldquo;Sign in to view&rdquo; placeholder inside the embed, not the
                  content.
                </span>
              </p>
            ) : null}

            {/* Live preview */}
            <div className="space-y-1.5">
              <Label className="text-xs text-muted-foreground">Preview</Label>
              <div className="overflow-hidden rounded-md border border-border bg-background">
                <iframe
                  src={embedUrl}
                  title={`Preview of ${title}`}
                  className="h-64 w-full"
                  loading="lazy"
                />
              </div>
            </div>
          </div>
        </DialogBody>
      </Dialog>
    </>
  );
}

/** A copy-to-clipboard button with a transient check state. */
function CopyButton({
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
