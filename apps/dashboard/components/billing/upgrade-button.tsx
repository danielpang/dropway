"use client";

import * as React from "react";
import { ArrowUpRight, Loader2 } from "lucide-react";

import { startUpgradeAction } from "@/app/(app)/billing/actions";
import { Button } from "@/components/ui/button";
import type { CheckoutTier, PlanTier } from "@/lib/api";
import { TIER_LABEL } from "@/lib/billing";
import { cn } from "@/lib/utils";

/**
 * Billing-page upgrade button: starts an upgrade to a self-serve tier and
 * redirects to the Stripe-hosted page. A Free org goes to Checkout; an org that
 * already pays goes to the Billing Portal's plan-change screen (one subscription
 * per org). Same path as the 402 modal's CTA. Nothing is granted on return; the
 * webhook flips plan_tier and the page's poller (FinalizingState) picks it up.
 */
export function UpgradeButton({
  targetTier,
  currentTier,
  className,
  block,
  label,
  variant,
  localCurrency,
}: {
  targetTier: CheckoutTier;
  /** The org's plan as this page knows it; decides Checkout vs the portal. */
  currentTier: PlanTier;
  className?: string;
  /** Stretch the button to fill its container (cards, table cells). */
  block?: boolean;
  /** Override the default "Upgrade to {tier}" label. */
  label?: string;
  variant?: React.ComponentProps<typeof Button>["variant"];
  /** Opt into Adaptive Pricing (local-currency presentment) for this checkout. */
  localCurrency?: boolean;
}) {
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  async function onClick() {
    setError(null);
    setPending(true);
    const result = await startUpgradeAction({ targetTier, currentTier, localCurrency });
    if (result.ok) {
      window.location.href = result.url;
      return;
    }
    setError(result.message);
    setPending(false);
  }

  return (
    <div className={cn(block && "w-full", className)}>
      <Button
        onClick={onClick}
        disabled={pending}
        aria-busy={pending}
        variant={variant}
        className={cn(block && "w-full")}
      >
        {pending ? <Loader2 className="animate-spin" aria-hidden /> : null}
        {label ?? `Upgrade to ${TIER_LABEL[targetTier]}`}
      </Button>
      {error && (
        <p role="alert" className="mt-2 text-sm text-destructive">
          {error}
        </p>
      )}
    </div>
  );
}

/** Top-of-ladder CTA: link out to sales (no self-serve checkout above Enterprise). */
export function ContactSalesButton({
  salesUrl,
  className,
  block,
}: {
  salesUrl?: string;
  className?: string;
  /** Stretch the button to fill its container (cards, table cells). */
  block?: boolean;
}) {
  const blockCls = block ? "w-full" : undefined;
  if (!salesUrl) {
    return (
      <Button
        variant="outline"
        disabled
        title="Sales contact not configured"
        className={cn(blockCls, className)}
      >
        Contact sales
      </Button>
    );
  }
  return (
    <Button asChild variant="outline" className={cn(blockCls, className)}>
      <a href={salesUrl} target="_blank" rel="noopener noreferrer">
        Contact sales
        <ArrowUpRight aria-hidden />
      </a>
    </Button>
  );
}
