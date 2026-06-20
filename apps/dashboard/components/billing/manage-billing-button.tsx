"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { CreditCard, Loader2 } from "lucide-react";

import { createPortalAction } from "@/app/(app)/billing/actions";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

/**
 * "Manage billing" → opens the Stripe Billing Portal. POSTs /v1/billing/portal
 * and full-page-redirects to the portal_url. A 409 (no Stripe customer yet)
 * means the org has never paid, we surface the inline hint and leave them on
 * the page to pick a plan instead.
 *
 * `hasSubscription` lets the caller render this only when a portal makes sense;
 * the button itself still handles a 409 defensively.
 */
export function ManageBillingButton({
  variant = "outline",
  block,
  label = "Manage billing",
  hideIcon,
}: {
  variant?: "default" | "outline";
  /** Stretch to fill its container (drawer cards). */
  block?: boolean;
  /** Override the default "Manage billing" label (e.g. "Downgrade to Pro"). */
  label?: string;
  hideIcon?: boolean;
}) {
  const router = useRouter();
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  async function onClick() {
    setError(null);
    setPending(true);
    const result = await createPortalAction();
    if (result.ok) {
      // Leave the SPA for the Stripe-hosted portal.
      window.location.href = result.portalUrl;
      return;
    }
    if (result.kind === "no_customer") {
      // Nudge them to refresh into the "pick a plan" view rather than the portal.
      setError(result.message);
      setPending(false);
      router.refresh();
      return;
    }
    setError(result.message);
    setPending(false);
  }

  return (
    <div className={cn("space-y-2", block && "w-full")}>
      <Button
        variant={variant}
        onClick={onClick}
        disabled={pending}
        aria-busy={pending}
        className={cn(block && "w-full")}
      >
        {pending ? (
          <Loader2 className="animate-spin" aria-hidden />
        ) : hideIcon ? null : (
          <CreditCard aria-hidden />
        )}
        {label}
      </Button>
      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
    </div>
  );
}
