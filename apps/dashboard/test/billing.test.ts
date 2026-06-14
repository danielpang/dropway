// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for the display-only billing helpers (lib/billing.ts) and the
// account-state gate (lib/billing-server.ts `isRestricted`). Nothing here is an
// entitlement — the real caps live in cloud/quota and plan_tier is webhook-only;
// these just keep the upgrade ladder / matrix / over-limit gating coherent.

import { describe, expect, it } from "vitest";

import {
  MATRIX_TIERS,
  PLAN_MATRIX,
  TIER_LABEL,
  isCheckoutTier,
  nextTier,
} from "@/lib/billing";
import { isRestricted } from "@/lib/billing-server";

describe("nextTier (upgrade ladder)", () => {
  it("walks free → business → enterprise → contact_sales", () => {
    expect(nextTier("free")).toBe("business");
    expect(nextTier("business")).toBe("enterprise");
    expect(nextTier("enterprise")).toBe("contact_sales");
  });

  it("has no further self-serve tier beyond the ladder (default branch)", () => {
    // An unexpected/unknown tier falls through to null (defensive default).
    expect(nextTier("contact_sales" as unknown as "free")).toBeNull();
    expect(nextTier("" as unknown as "free")).toBeNull();
  });
});

describe("isCheckoutTier (self-serve vs contact-sales)", () => {
  it("is true only for the self-serve paid tiers", () => {
    expect(isCheckoutTier("business")).toBe(true);
    expect(isCheckoutTier("enterprise")).toBe(true);
  });

  it("is false for free, contact_sales, and nullish/garbage inputs", () => {
    expect(isCheckoutTier("free")).toBe(false);
    expect(isCheckoutTier("contact_sales")).toBe(false);
    expect(isCheckoutTier(undefined)).toBe(false);
    expect(isCheckoutTier(null)).toBe(false);
    expect(isCheckoutTier("BUSINESS")).toBe(false); // case-sensitive
  });
});

describe("TIER_LABEL + matrix tables", () => {
  it("labels every display tier including the top contact-sales rung", () => {
    expect(TIER_LABEL.free).toBe("Free");
    expect(TIER_LABEL.business).toBe("Business");
    expect(TIER_LABEL.enterprise).toBe("Enterprise");
    expect(TIER_LABEL.contact_sales).toBe("Enterprise+");
  });

  it("MATRIX_TIERS is the three concrete plan tiers in display order", () => {
    expect(MATRIX_TIERS).toEqual(["free", "business", "enterprise"]);
  });

  it("every PLAN_MATRIX row has a value for each concrete tier", () => {
    expect(PLAN_MATRIX.length).toBeGreaterThan(0);
    for (const row of PLAN_MATRIX) {
      expect(typeof row.label).toBe("string");
      for (const tier of MATRIX_TIERS) {
        // No missing cells — the grid must render fully for all three columns.
        expect(typeof row.values[tier]).toBe("string");
        expect(row.values[tier].length).toBeGreaterThan(0);
      }
    }
  });

  it("encodes the §9 free-tier caps (5 members, 10 sites/user)", () => {
    const members = PLAN_MATRIX.find((r) => r.label === "Members / org");
    const sites = PLAN_MATRIX.find((r) => r.label === "Sites / user");
    expect(members?.values.free).toBe("Up to 5");
    expect(sites?.values.free).toBe("10");
  });
});

describe("isRestricted (billing-derived read-only gate)", () => {
  it("pauses new cost-creating actions for over_limit / past_due / suspended", () => {
    expect(isRestricted("over_limit")).toBe(true);
    expect(isRestricted("past_due")).toBe(true);
    expect(isRestricted("suspended")).toBe(true);
  });

  it("allows actions for an active org (and unknown non-blocking states)", () => {
    expect(isRestricted("active")).toBe(false);
    expect(isRestricted("trialing" as unknown as "active")).toBe(false);
  });
});
