"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { Loader2, Plus } from "lucide-react";

import { createSiteAction } from "@/app/(app)/dashboard/actions";
import { UpgradeModal } from "@/components/sites/upgrade-modal";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogBody,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { QuotaExceeded } from "@/lib/api";

/** A slug is a single DNS label: lowercase alphanumerics + hyphens. */
function slugify(value: string): string {
  return value
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, "-")
    .replace(/-+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 63);
}

const SLUG_RE = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;

/**
 * "New site" affordance: a trigger button + a dialog that POSTs to the Go API
 * via the create-site server action. On a 402 it closes and opens the upgrade
 * modal placeholder, reading the quota body the API returned. On success it
 * navigates to the new site's detail page.
 */
export function NewSiteDialog() {
  const router = useRouter();

  const [open, setOpen] = React.useState(false);
  const [slug, setSlug] = React.useState("");
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [touched, setTouched] = React.useState(false);

  // Quota cap surfaced by a 402 → drives the upgrade modal placeholder.
  const [quota, setQuota] = React.useState<QuotaExceeded | null>(null);

  const slugValid = SLUG_RE.test(slug);

  function reset() {
    setSlug("");
    setError(null);
    setTouched(false);
    setPending(false);
  }

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setTouched(true);
    if (!slugValid) return;

    setPending(true);
    const result = await createSiteAction({ slug });

    if (result.ok) {
      setOpen(false);
      reset();
      router.push(`/sites/${result.site.id}`);
      router.refresh();
      return;
    }

    if (result.kind === "quota") {
      // Hand off to the upgrade modal; close the create dialog behind it.
      setOpen(false);
      reset();
      setQuota(result.quota);
      return;
    }

    setError(result.message);
    setPending(false);
  }

  return (
    <>
      <Button onClick={() => setOpen(true)}>
        <Plus aria-hidden />
        New site
      </Button>

      <Dialog
        open={open}
        onOpenChange={(next) => {
          setOpen(next);
          if (!next) reset();
        }}
      >
        <DialogHeader>
          <DialogTitle>Create a new site</DialogTitle>
          <DialogDescription>
            Pick a slug — it becomes your site&rsquo;s subdomain. You can deploy
            content from the CLI right after.
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={onSubmit}>
          <DialogBody>
            <div className="space-y-2">
              <Label htmlFor="site-slug">Slug</Label>
              <div className="flex items-center gap-2">
                <Input
                  id="site-slug"
                  name="site-slug"
                  placeholder="my-docs"
                  value={slug}
                  onChange={(e) => setSlug(slugify(e.target.value))}
                  aria-invalid={touched && !slugValid}
                  aria-describedby="slug-help"
                  className="font-mono"
                  autoFocus
                  disabled={pending}
                />
                <span className="whitespace-nowrap text-sm text-muted-foreground">
                  .shippedusercontent.com
                </span>
              </div>
              <p id="slug-help" className="text-xs text-muted-foreground">
                Lowercase letters, numbers, and hyphens.
              </p>
              {touched && !slugValid && (
                <p className="text-xs text-destructive">
                  Enter a valid slug (e.g. my-docs).
                </p>
              )}
              {error && (
                <p
                  role="alert"
                  className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
                >
                  {error}
                </p>
              )}
            </div>
          </DialogBody>

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => {
                setOpen(false);
                reset();
              }}
              disabled={pending}
            >
              Cancel
            </Button>
            <Button type="submit" disabled={pending} aria-busy={pending}>
              {pending ? <Loader2 className="animate-spin" aria-hidden /> : null}
              Create site
            </Button>
          </DialogFooter>
        </form>
      </Dialog>

      <UpgradeModal
        quota={quota}
        open={quota !== null}
        onOpenChange={(next) => {
          if (!next) setQuota(null);
        }}
      />
    </>
  );
}
