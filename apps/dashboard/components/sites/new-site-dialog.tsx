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
import { Tooltip } from "@/components/ui/tooltip";
import type { QuotaExceeded } from "@/lib/api";

/**
 * A slug is a single DNS label: lowercase alphanumerics + hyphens. Used to
 * normalize the field on EVERY keystroke, so it must keep a single TRAILING hyphen,
 * otherwise typing "my-docs" loses the "-" the instant it's typed (it's trailing
 * until the next char) and a hyphenated slug becomes impossible. Leading hyphens and
 * runs are still collapsed; a stray trailing hyphen is caught by SLUG_RE on submit.
 */
function slugify(value: string): string {
  return value
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, "-")
    .replace(/-+/g, "-")
    .replace(/^-+/g, "")
    .slice(0, 63);
}

const SLUG_RE = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;

// useLayoutEffect on the client (flicker-free caret restore), useEffect on the
// server (avoids the "useLayoutEffect does nothing on the server" SSR warning).
const useIsomorphicLayoutEffect =
  typeof window !== "undefined" ? React.useLayoutEffect : React.useEffect;

/**
 * "New site" affordance: a trigger button + a dialog that POSTs to the Go API
 * via the create-site server action. On a 402 it closes and opens the upgrade
 * modal, reading the quota body the API returned (→ Stripe Checkout). On success
 * it navigates to the new site's detail page.
 *
 * When `readOnly` is set (org is over_limit / past_due / suspended) the
 * trigger is DISABLED with an explanatory tooltip and the dialog can't open, * a UX mirror of the server-side restriction (the API would 402/403 anyway).
 */
export function NewSiteDialog({
  readOnly = false,
  orgSlug = null,
}: {
  readOnly?: boolean;
  /** The active org's slug, content hosts are `<org-slug>-<site-slug>.dropwaycontent.com`. */
  orgSlug?: string | null;
}) {
  const router = useRouter();

  const [open, setOpen] = React.useState(false);
  const [slug, setSlug] = React.useState("");
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [touched, setTouched] = React.useState(false);

  // Quota cap surfaced by a 402 → drives the upgrade modal placeholder.
  const [quota, setQuota] = React.useState<QuotaExceeded | null>(null);

  const slugValid = SLUG_RE.test(slug);

  // The slug field reformats its value (slugify) on every keystroke, and a controlled
  // input resets the caret to the END whenever its value is set programmatically, so
  // editing mid-string would fling the cursor to the end after each character. Capture
  // the intended caret offset on change and restore it once the DOM has the new value.
  const inputRef = React.useRef<HTMLInputElement>(null);
  const caretRef = React.useRef<number | null>(null);

  useIsomorphicLayoutEffect(() => {
    const pos = caretRef.current;
    if (pos != null && inputRef.current) {
      inputRef.current.setSelectionRange(pos, pos);
      caretRef.current = null;
    }
  });

  function onSlugChange(e: React.ChangeEvent<HTMLInputElement>) {
    const raw = e.target.value;
    const caret = e.target.selectionStart ?? raw.length;
    // The caret belongs just after the slugified text that preceded it, so an inserted
    // or edited character keeps the cursor beside it instead of jumping to the end.
    caretRef.current = slugify(raw.slice(0, caret)).length;
    setSlug(slugify(raw));
  }

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

  if (readOnly) {
    // Account is restricted by billing: disable the action and explain why.
    // `disabled` buttons don't emit focus/hover, so the Tooltip wrapper (its
    // own focusable span) carries the hover/focus + aria-describedby.
    return (
      <Tooltip label="Your organization is over its plan limit. Visit Billing to upgrade before creating new sites.">
        <Button disabled aria-disabled className="pointer-events-none">
          <Plus aria-hidden />
          New site
        </Button>
      </Tooltip>
    );
  }

  return (
    <>
      <Button onClick={() => setOpen(true)}>
        <Plus aria-hidden />
        New site
      </Button>

      <Dialog
        className="max-w-xl"
        open={open}
        onOpenChange={(next) => {
          setOpen(next);
          if (!next) reset();
        }}
      >
        <DialogHeader>
          <DialogTitle>Create a new site</DialogTitle>
          <DialogDescription>
            Pick a slug. Combined with your org slug, it becomes your
            site&rsquo;s subdomain. You can deploy content from the CLI right
            after.
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={onSubmit}>
          <DialogBody>
            <div className="space-y-2">
              <Label htmlFor="site-slug">Slug</Label>
              <div className="flex items-center gap-1.5">
                {orgSlug ? (
                  <span className="shrink-0 whitespace-nowrap font-mono text-sm text-muted-foreground">
                    {orgSlug}-
                  </span>
                ) : null}
                <Input
                  ref={inputRef}
                  id="site-slug"
                  name="site-slug"
                  placeholder="my-docs"
                  value={slug}
                  onChange={onSlugChange}
                  aria-invalid={touched && !slugValid}
                  aria-describedby="slug-help"
                  className="min-w-0 flex-1 font-mono"
                  autoFocus
                  disabled={pending}
                />
                <span className="shrink-0 whitespace-nowrap font-mono text-sm text-muted-foreground">
                  .dropwaycontent.com
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
