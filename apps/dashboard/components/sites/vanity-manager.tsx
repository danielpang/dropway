"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { Check, ExternalLink, Loader2, Trash2 } from "lucide-react";

import {
  registerVanityAction,
  releaseVanityAction,
} from "@/app/(app)/sites/[id]/domains/actions";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

/** Mirrors the server's slug grammar for fast client feedback (single lowercase
 * DNS label, no `--`); the Go API is authoritative and also enforces the
 * reserved-word blocklist. */
const SLUG_RE = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/;

function slugify(input: string): string {
  return input
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, "-")
    .replace(/-+/g, "-")
    .replace(/^-|-$/g, "")
    .slice(0, 63);
}

/**
 * Claim / release the site's vanity platform subdomain
 * (<slug>.dropwaycontent.com). Available on every deployment: a vanity host
 * needs no Cloudflare provisioning or DNS verification, it serves off the
 * platform wildcard the moment it's claimed.
 */
export function VanityManager({
  siteId,
  vanityHost,
  contentDomain,
  liveUrl,
  disabled,
}: {
  siteId: string;
  /** The currently claimed host (e.g. "readme.dropwaycontent.com"), null when none. */
  vanityHost: string | null;
  /** The platform content domain suffix shown after the input (e.g. "dropwaycontent.com"). */
  contentDomain: string;
  /** The live URL for the claimed host (server-built scheme/port), null when none. */
  liveUrl: string | null;
  disabled: boolean;
}) {
  const router = useRouter();
  const [slug, setSlug] = React.useState("");
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  const slugValid = SLUG_RE.test(slug) && !slug.includes("--");

  async function onClaim(e: React.FormEvent) {
    e.preventDefault();
    if (!slugValid || pending) return;
    setPending(true);
    setError(null);
    const res = await registerVanityAction({ siteId, slug });
    setPending(false);
    if (!res.ok) {
      setError(res.message);
      return;
    }
    setSlug("");
    router.refresh();
  }

  async function onRelease() {
    if (pending) return;
    setPending(true);
    setError(null);
    const res = await releaseVanityAction({ siteId });
    setPending(false);
    if (!res.ok) {
      setError(res.message);
      return;
    }
    router.refresh();
  }

  if (vanityHost) {
    return (
      <div className="space-y-3">
        <div className="flex items-center justify-between gap-3 rounded-md border px-3 py-2.5">
          <div className="flex min-w-0 items-center gap-2">
            <Check className="size-4 shrink-0 text-emerald-600 dark:text-emerald-400" aria-hidden />
            <a
              href={liveUrl ?? `https://${vanityHost}`}
              target="_blank"
              rel="noreferrer"
              className="inline-flex min-w-0 items-center gap-1.5 font-mono text-sm text-foreground hover:underline"
            >
              <span className="truncate">{vanityHost}</span>
              <ExternalLink className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
            </a>
          </div>
          {!disabled && (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={onRelease}
              disabled={pending}
              aria-busy={pending}
            >
              {pending ? (
                <Loader2 className="size-4 animate-spin" aria-hidden />
              ) : (
                <Trash2 className="size-4" aria-hidden />
              )}
              Release
            </Button>
          )}
        </div>
        {error && <p className="text-sm text-destructive">{error}</p>}
        <p className="text-xs text-muted-foreground">
          Releasing frees the name for anyone to claim. Your site stays reachable
          at its standard address either way.
        </p>
      </div>
    );
  }

  return (
    <form onSubmit={onClaim} className="space-y-3">
      <div className="space-y-2">
        <Label htmlFor="vanity-slug">Subdomain</Label>
        <div className="flex items-center gap-1.5">
          <Input
            id="vanity-slug"
            name="vanity-slug"
            placeholder="my-site"
            value={slug}
            onChange={(e) => setSlug(slugify(e.target.value))}
            className="min-w-0 flex-1 font-mono"
            disabled={disabled || pending}
          />
          <span className="shrink-0 whitespace-nowrap font-mono text-sm text-muted-foreground">
            .{contentDomain}
          </span>
          <Button
            type="submit"
            disabled={disabled || pending || !slugValid}
            aria-busy={pending}
          >
            {pending && <Loader2 className="size-4 animate-spin" aria-hidden />}
            Claim
          </Button>
        </div>
      </div>
      {error && <p className="text-sm text-destructive">{error}</p>}
    </form>
  );
}
