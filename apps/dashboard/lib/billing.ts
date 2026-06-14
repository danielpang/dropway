import type { PlanTier } from "@/lib/api";

/**
 * Display-only billing metadata for the dashboard (architecture §9).
 *
 * IMPORTANT: nothing here is an entitlement. The plan limits below are a
 * marketing/UX matrix; the REAL caps are enforced server-side in `cloud/quota`
 * (the OSS build is unlimited) and the authoritative `plan_tier` is written to
 * the DB ONLY by the signature-verified Stripe webhook — never by the browser.
 * This module just renders the ladder and maps a tier to its "next" upgrade
 * target so the 402 modal and the billing page agree on copy.
 */

/** A tier the UI can show, including the top "contact sales" rung (not a DB plan_tier). */
export type DisplayTier = PlanTier | "contact_sales";

/** Human label for a tier (used in CTAs, the plan card, and the matrix header). */
export const TIER_LABEL: Record<DisplayTier, string> = {
  free: "Free",
  business: "Business",
  enterprise: "Enterprise",
  contact_sales: "Enterprise+",
};

/**
 * The upgrade ladder: free → business → enterprise → contact_sales. The 402
 * body carries its own `next_tier`/`sales_url` (server-authoritative); this is
 * the fallback the billing page uses to render "upgrade to X" when there's no
 * 402 in hand. Top of the ladder has no further self-serve tier.
 */
export function nextTier(tier: PlanTier): DisplayTier | null {
  switch (tier) {
    case "free":
      return "business";
    case "business":
      return "enterprise";
    case "enterprise":
      return "contact_sales";
    default:
      return null;
  }
}

/** True when a target tier is self-serve (Checkout) vs. a Contact Sales motion. */
export function isCheckoutTier(
  tier: DisplayTier | string | undefined | null,
): tier is "business" | "enterprise" {
  return tier === "business" || tier === "enterprise";
}

/** A single row of the plan/limits matrix shown on the billing page. */
export interface PlanFeatureRow {
  label: string;
  /** Value per tier, indexed by the three concrete plan tiers. */
  values: Record<PlanTier, string>;
}

/**
 * The plan/limits matrix (architecture §9 bands), free → business → enterprise.
 * Display-only; mirrors the §9 table so paying clearly raises the caps. The
 * "Contact Sales" column lives in its own CTA, not this grid.
 */
export const PLAN_MATRIX: PlanFeatureRow[] = [
  {
    label: "Members / org",
    values: { free: "Up to 5", business: "Up to 99", enterprise: "Up to 1,000" },
  },
  {
    label: "Sites / user",
    values: { free: "10", business: "100", enterprise: "1,000" },
  },
  {
    label: "Deploys / mo",
    values: { free: "100", business: "5,000", enterprise: "50,000" },
  },
  {
    label: "Bandwidth / mo",
    values: { free: "10 GB", business: "250 GB", enterprise: "2 TB pooled" },
  },
  {
    label: "Storage",
    values: { free: "1 GB", business: "100 GB", enterprise: "500 GB" },
  },
  {
    label: "Custom domains",
    values: { free: "0", business: "5", enterprise: "50" },
  },
  {
    label: "Sharing tiers",
    values: {
      free: "Public · org · link",
      business: "+ Password / unlisted",
      enterprise: "+ IP allowlist",
    },
  },
  {
    label: "SSO / SAML",
    values: { free: "—", business: "—", enterprise: "Included" },
  },
  {
    label: "Audit logs",
    values: { free: "—", business: "30-day", enterprise: "Export" },
  },
  {
    label: "Support",
    values: { free: "Community", business: "Email", enterprise: "Priority + SLA" },
  },
  {
    label: "Price",
    values: { free: "$0", business: "Per-seat", enterprise: "Per-seat or invoiced" },
  },
];

/** The plan tiers in matrix order (columns of the grid). */
export const MATRIX_TIERS: PlanTier[] = ["free", "business", "enterprise"];
