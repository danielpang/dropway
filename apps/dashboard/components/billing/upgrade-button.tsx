"use client";

import * as React from "react";
import { ArrowUpRight, Loader2 } from "lucide-react";

import { createCheckoutAction } from "@/app/(app)/billing/actions";
import { Button } from "@/components/ui/button";
import type { CheckoutTier } from "@/lib/api";
import { TIER_LABEL } from "@/lib/billing";

/**
 * Billing-page upgrade button: starts Checkout for a self-serve tier and
 * redirects to the Stripe-hosted URL. Same path as the 402 modal's CTA, the
 * success redirect grants nothing; the webhook flips plan_tier and the page's
 * poller (FinalizingState) picks it up.
 */
export function UpgradeButton({
  targetTier,
  className,
}: {
  targetTier: CheckoutTier;
  className?: string;
}) {
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  async function onClick() {
    setError(null);
    setPending(true);
    const result = await createCheckoutAction({ targetTier });
    if (result.ok) {
      window.location.href = result.checkoutUrl;
      return;
    }
    setError(result.message);
    setPending(false);
  }

  return (
    <div className={className}>
      <Button onClick={onClick} disabled={pending} aria-busy={pending}>
        {pending ? <Loader2 className="animate-spin" aria-hidden /> : null}
        {`Upgrade to ${TIER_LABEL[targetTier]}`}
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
export function ContactSalesButton({ salesUrl }: { salesUrl?: string }) {
  if (!salesUrl) {
    return (
      <Button variant="outline" disabled title="Sales contact not configured">
        Contact sales
      </Button>
    );
  }
  return (
    <Button asChild variant="outline">
      <a href={salesUrl} target="_blank" rel="noopener noreferrer">
        Contact sales
        <ArrowUpRight aria-hidden />
      </a>
    </Button>
  );
}
