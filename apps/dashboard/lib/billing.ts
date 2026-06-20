import type { PlanTier } from "@/lib/api";

/**
 * Display-only billing metadata for the dashboard.
 *
 * IMPORTANT: nothing here is an entitlement. The plan limits below are a
 * marketing/UX matrix; the REAL caps are enforced server-side in `cloud/quota`
 * (the OSS build is unlimited) and the authoritative `plan_tier` is written to
 * the DB ONLY by the signature-verified Stripe webhook, never by the browser.
 * This module just renders the ladder and maps a tier to its "next" upgrade
 * target so the 402 modal and the billing page agree on copy.
 */

/** A tier the UI can show, including the top "contact sales" rung (not a DB plan_tier). */
export type DisplayTier = PlanTier | "contact_sales";

/**
 * Human label for a tier (used in CTAs, the plan card, and the matrix header).
 * The internal plan_tier keys now match the public labels one-to-one
 * (free / pro / business / enterprise); "contact_sales" is the display-only rung
 * above Enterprise.
 */
export const TIER_LABEL: Record<DisplayTier, string> = {
  free: "Free",
  pro: "Pro",
  business: "Business",
  enterprise: "Enterprise",
  contact_sales: "Enterprise+",
};

/**
 * The upgrade ladder: free → pro → business → enterprise → contact_sales. The 402
 * body carries its own `next_tier`/`sales_url` (server-authoritative); this is
 * the fallback the billing page uses to render "upgrade to X" when there's no
 * 402 in hand. Top of the ladder has no further self-serve tier.
 */
export function nextTier(tier: PlanTier): DisplayTier | null {
  switch (tier) {
    case "free":
      return "pro";
    case "pro":
      return "business";
    case "business":
      return "enterprise";
    case "enterprise":
      return "contact_sales";
    default:
      return null;
  }
}

/**
 * True when a target tier is self-serve (Stripe Checkout) vs. a Contact Sales
 * motion. Pro ($25) and Business ($150) are self-serve; Enterprise is "Custom"
 * (Contact Sales), so it is NOT a checkout tier here.
 */
export function isCheckoutTier(
  tier: DisplayTier | string | undefined | null,
): tier is "pro" | "business" {
  return tier === "pro" || tier === "business";
}

/** A single row of the plan/limits matrix shown on the billing page. */
export interface PlanFeatureRow {
  label: string;
  /** Value per tier, indexed by the four concrete plan tiers. */
  values: Record<PlanTier, string>;
}

/**
 * The plan/limits matrix, free → Pro → Business → Enterprise. Seat-free: you pay
 * for capacity (sites), not seats, and team members are unlimited on every plan.
 * Business is the $150 unlimited-sites tier between Pro and Enterprise. Display-only;
 * the REAL site cap is enforced server-side in cloud/quota (ResourceSitePerOrg). The
 * "Contact Sales" rung lives in its own CTA, not this grid.
 */
export const PLAN_MATRIX: PlanFeatureRow[] = [
  {
    label: "Sites / workspace",
    values: {
      free: "Up to 10",
      pro: "Up to 100",
      business: "Unlimited",
      enterprise: "Unlimited",
    },
  },
  {
    label: "Team members",
    values: {
      free: "Unlimited",
      pro: "Unlimited",
      business: "Unlimited",
      enterprise: "Unlimited",
    },
  },
  {
    label: "Deploy via dashboard, CLI & MCP",
    values: {
      free: "Included",
      pro: "Included",
      business: "Included",
      enterprise: "Included",
    },
  },
  {
    label: "Sharing tiers",
    values: {
      free: "Public · org · password · allowlist",
      pro: "All tiers",
      business: "All tiers",
      enterprise: "All tiers",
    },
  },
  {
    label: "Custom domains",
    values: {
      free: "Not included",
      pro: "Included",
      business: "Included",
      enterprise: "Included",
    },
  },
  {
    label: "Version history & instant rollback",
    values: {
      free: "Included",
      pro: "Included",
      business: "Included",
      enterprise: "Included",
    },
  },
  {
    label: "SSO / SAML & SCIM",
    values: {
      free: "Not included",
      pro: "Not included",
      business: "Not included",
      enterprise: "Included",
    },
  },
  {
    label: "Audit logs & advanced RBAC",
    values: {
      free: "Not included",
      pro: "Not included",
      business: "Not included",
      enterprise: "Included",
    },
  },
  {
    label: "Support",
    values: {
      free: "Community",
      pro: "Priority email",
      business: "Priority email",
      enterprise: "Priority + 99.9% SLA & DPA",
    },
  },
  {
    label: "Price",
    values: {
      free: "$0",
      pro: "$25 / mo",
      business: "$150 / mo",
      enterprise: "Custom",
    },
  },
];

/** The plan tiers in matrix order (columns of the grid). */
export const MATRIX_TIERS: PlanTier[] = ["free", "pro", "business", "enterprise"];
