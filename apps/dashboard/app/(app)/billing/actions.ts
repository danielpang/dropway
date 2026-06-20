"use server";

import { api, ApiError, type BillingPlan, type CheckoutTier } from "@/lib/api";

/**
 * Billing server actions (CLOUD-ONLY surface). These call the
 * Go API's /v1/billing/* endpoints carrying the caller's EdDSA JWT; the API is
 * the authz boundary and independently re-checks owner/admin on every WRITE
 * (checkout/portal), the dashboard's role gate is UX only, never trusted.
 *
 * Server actions can't throw rich typed errors across the boundary, so each
 * returns a discriminated union the client maps to a redirect or an inline
 * message. CRITICAL: none of these grant entitlement, they only START a
 * Stripe-hosted flow. plan_tier flips ONLY via the signed webhook.
 */

export type CheckoutActionResult =
  | { ok: true; checkoutUrl: string }
  | { ok: false; message: string };

/**
 * Start a Stripe Checkout session for a paid tier and hand back the hosted URL
 * for the client to redirect to. Used by the 402 upgrade modal and the billing
 * page's upgrade buttons.
 */
export async function createCheckoutAction(input: {
  targetTier: CheckoutTier;
  seats?: number;
  /** Opt into Adaptive Pricing (local-currency presentment) vs USD. */
  localCurrency?: boolean;
}): Promise<CheckoutActionResult> {
  try {
    const { checkout_url } = await api.createCheckout({
      target_tier: input.targetTier,
      ...(input.seats ? { seats: input.seats } : {}),
      ...(input.localCurrency ? { local_currency: true } : {}),
    });
    if (!checkout_url) {
      return { ok: false, message: "Stripe did not return a checkout URL. Try again." };
    }
    return { ok: true, checkoutUrl: checkout_url };
  } catch (err) {
    return { ok: false, message: checkoutErrorMessage(err) };
  }
}

export type PortalActionResult =
  | { ok: true; portalUrl: string }
  | { ok: false; kind: "no_customer" | "error"; message: string };

/**
 * Open the Stripe Billing Portal for the org's existing Customer. A 409 means
 * the org has never checked out (no Stripe customer yet), the UI should route
 * the user to Checkout instead of the portal.
 */
export async function createPortalAction(): Promise<PortalActionResult> {
  try {
    const { portal_url } = await api.createPortal();
    if (!portal_url) {
      return { ok: false, kind: "error", message: "Stripe did not return a portal URL. Try again." };
    }
    return { ok: true, portalUrl: portal_url };
  } catch (err) {
    if (err instanceof ApiError && err.status === 409) {
      return {
        ok: false,
        kind: "no_customer",
        message: "No subscription yet. Start a plan first to manage billing.",
      };
    }
    return { ok: false, kind: "error", message: writeErrorMessage(err) };
  }
}

export type BillingPlanResult =
  | { ok: true; plan: BillingPlan }
  // The cloud build isn't present (self-host) OR billing is unreachable. The UI
  // treats this as "no billing" and hides upgrade affordances.
  | { ok: false; kind: "unavailable" };

/**
 * Read the org's current plan. Used by the billing page's "finalizing your
 * subscription…" poller after returning from Stripe: the entitlement lands via
 * the WEBHOOK, not the success redirect, so the page polls this until plan_tier
 * flips.
 */
export async function getBillingPlanAction(): Promise<BillingPlanResult> {
  try {
    const plan = await api.getBilling();
    return { ok: true, plan };
  } catch {
    return { ok: false, kind: "unavailable" };
  }
}

function checkoutErrorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    const apiMsg = (err.body as { message?: string } | null)?.message;
    if (apiMsg) return apiMsg;
    if (err.status === 403) {
      return "Only owners and admins can change the plan.";
    }
    return "Could not start checkout. Try again.";
  }
  return "Could not reach the billing API. Try again.";
}

function writeErrorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    const apiMsg = (err.body as { message?: string } | null)?.message;
    if (apiMsg) return apiMsg;
    if (err.status === 403) {
      return "Only owners and admins can manage billing.";
    }
    return "Could not open the billing portal. Try again.";
  }
  return "Could not reach the billing API. Try again.";
}
