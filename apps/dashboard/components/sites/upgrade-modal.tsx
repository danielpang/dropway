"use client";

import * as React from "react";
import { ArrowUpRight, Loader2, Zap } from "lucide-react";

import { createCheckoutAction } from "@/app/(app)/billing/actions";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogBody,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import type { QuotaExceeded } from "@/lib/api";
import { isCheckoutTier, TIER_LABEL, type DisplayTier } from "@/lib/billing";

/** Human label for each capped resource the API can report. */
const RESOURCE_LABEL: Record<string, string> = {
  sites_per_user: "sites",
  members_per_org: "team members",
};

/** Pretty label for the next tier the 402 points at (falls back to the raw string). */
function tierLabel(tier: string | undefined): string {
  if (!tier) return "the next plan";
  return TIER_LABEL[tier as DisplayTier] ?? tier;
}

/**
 * Upgrade modal (Phase 3: wired to Stripe Checkout). It renders the 402
 * `quota.ExceededError` body the Go API returns so the user sees the exact cap
 * they hit, then routes them to the right next step:
 *
 *  - next_tier is a self-serve tier (business / enterprise) → "Upgrade to X"
 *    POSTs /v1/billing/checkout {target_tier} and redirects to the Stripe-hosted
 *    checkout_url. (The success redirect grants NOTHING — only the signed
 *    webhook flips plan_tier; the billing page polls for that. §9.)
 *  - next_tier === 'contact_sales' (above Enterprise) → no checkout; show a
 *    Contact Sales CTA linking to the API-provided sales_url.
 *
 * The API's `next_tier` / `sales_url` are authoritative; we only style them.
 */
export function UpgradeModal({
  quota,
  open,
  onOpenChange,
}: {
  quota: QuotaExceeded | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  // Reset the transient checkout state whenever the modal is (re)opened.
  React.useEffect(() => {
    if (open) {
      setPending(false);
      setError(null);
    }
  }, [open]);

  if (!quota) return null;

  const resource = quota.limit
    ? RESOURCE_LABEL[quota.limit] ?? quota.limit
    : "resources";
  const nextTier = quota.next_tier;
  const isContactSales = nextTier === "contact_sales";
  const checkoutTier = isCheckoutTier(nextTier) ? nextTier : null;

  async function onUpgrade() {
    if (!checkoutTier) return;
    setError(null);
    setPending(true);
    const result = await createCheckoutAction({ targetTier: checkoutTier });
    if (result.ok) {
      // Full-page redirect to Stripe-hosted Checkout (leaves the SPA).
      window.location.href = result.checkoutUrl;
      return;
    }
    setError(result.message);
    setPending(false);
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogHeader>
        <div className="mb-1 grid size-10 place-items-center rounded-lg bg-secondary text-secondary-foreground">
          <Zap className="size-5" aria-hidden />
        </div>
        <DialogTitle>You&rsquo;ve hit your plan limit</DialogTitle>
        <DialogDescription>
          {`Your ${quota.plan_tier ?? "current"} plan allows ${
            quota.max ?? "a limited number of"
          } ${resource}.`}
        </DialogDescription>
      </DialogHeader>

      <DialogBody>
        <dl className="grid grid-cols-2 gap-px overflow-hidden rounded-md border border-border bg-border text-sm">
          <Stat label="In use" value={String(quota.current ?? "—")} />
          <Stat label="Included" value={String(quota.max ?? "—")} />
        </dl>

        {isContactSales ? (
          <p className="mt-3 text-sm text-muted-foreground">
            You&rsquo;re past the self-serve tiers. Talk to our team about an{" "}
            <span className="font-medium text-foreground">Enterprise</span> plan
            sized for your organization.
          </p>
        ) : nextTier ? (
          <p className="mt-3 text-sm text-muted-foreground">
            Move to the{" "}
            <span className="font-medium text-foreground">
              {tierLabel(nextTier)}
            </span>{" "}
            plan to raise this limit.
          </p>
        ) : null}

        {error && (
          <p
            role="alert"
            className="mt-3 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
          >
            {error}
          </p>
        )}
      </DialogBody>

      <DialogFooter>
        <Button
          variant="outline"
          onClick={() => onOpenChange(false)}
          disabled={pending}
        >
          Not now
        </Button>

        {isContactSales ? (
          // Top of the ladder: no checkout, link out to sales.
          quota.sales_url ? (
            <Button asChild>
              <a href={quota.sales_url} target="_blank" rel="noopener noreferrer">
                Contact sales
                <ArrowUpRight aria-hidden />
              </a>
            </Button>
          ) : (
            <Button disabled title="Sales contact not configured">
              Contact sales
            </Button>
          )
        ) : checkoutTier ? (
          <Button onClick={onUpgrade} disabled={pending} aria-busy={pending}>
            {pending ? <Loader2 className="animate-spin" aria-hidden /> : null}
            {`Upgrade to ${tierLabel(checkoutTier)}`}
          </Button>
        ) : (
          // No machine next_tier (shouldn't normally happen) — degrade to a
          // link to the billing page so the user can still self-serve.
          <Button asChild>
            <a href="/billing">View plans</a>
          </Button>
        )}
      </DialogFooter>
    </Dialog>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="bg-card p-3">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-0.5 text-lg font-semibold tabular-nums text-foreground">
        {value}
      </dd>
    </div>
  );
}
