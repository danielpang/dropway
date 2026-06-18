import type { PlanTier } from "@/lib/api";

/**
 * Display-only billing metadata for the dashboard.
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

/**
 * Human label for a tier (used in CTAs, the plan card, and the matrix header).
 * NOTE: the internal plan_tier "business" is presented as "Pro" everywhere in the
 * UI and on the marketing site (the DB/Stripe key stays "business").
 */
export const TIER_LABEL: Record<DisplayTier, string> = {
  free: "Free",
  business: "Pro",
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
 * The plan/limits matrix, free → Pro → Enterprise. Seat-free: you pay for SITES,
 * not seats, so the lever is the per-ORG site count and team
 * members are unlimited on every plan. Display-only; the REAL site cap is enforced
 * server-side in cloud/quota (ResourceSitePerOrg). The "Contact Sales" rung lives in
 * its own CTA, not this grid.
 */
export const PLAN_MATRIX: PlanFeatureRow[] = [
  {
    label: "Sites / workspace",
    values: { free: "Up to 10", business: "Up to 100", enterprise: "Unlimited" },
  },
  {
    label: "Team members",
    values: { free: "Unlimited", business: "Unlimited", enterprise: "Unlimited" },
  },
  {
    label: "Deploy via dashboard, CLI & MCP",
    values: { free: "Included", business: "Included", enterprise: "Included" },
  },
  {
    label: "Sharing tiers",
    values: {
      free: "Public · org · password · allowlist",
      business: "All tiers",
      enterprise: "All tiers",
    },
  },
  {
    label: "Custom domains",
    values: { free: "—", business: "Included", enterprise: "Included" },
  },
  {
    label: "Version history & instant rollback",
    values: { free: "Included", business: "Included", enterprise: "Included" },
  },
  {
    label: "SSO / SAML & SCIM",
    values: { free: "—", business: "—", enterprise: "Included" },
  },
  {
    label: "Audit logs & advanced RBAC",
    values: { free: "—", business: "—", enterprise: "Included" },
  },
  {
    label: "Support",
    values: {
      free: "Community",
      business: "Priority email",
      enterprise: "Priority + 99.9% SLA & DPA",
    },
  },
  {
    label: "Price",
    values: { free: "$0", business: "$25 / mo flat", enterprise: "Custom" },
  },
];

/** The plan tiers in matrix order (columns of the grid). */
export const MATRIX_TIERS: PlanTier[] = ["free", "business", "enterprise"];
