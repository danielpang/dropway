import "server-only";

import { cache } from "react";

import { api, ApiError, type OrgStatus } from "@/lib/api";

/**
 * Server-side read of the org's billing-derived account state.
 *
 * Drives the over-limit banner (app shell) and the read-only gating of
 * cost-creating actions like "New site". This is a UX MIRROR of server-side
 * enforcement, the Go API / cloud quota gate is the real boundary; this just
 * lets the dashboard show the restriction honestly instead of letting the user
 * hit a 402/403 blindly.
 *
 * On the OSS/self-host build /v1/billing 404s (no cloud) → status "active"
 * (unlimited, never restricted). Any other read failure also degrades to
 * "active" so a billing-API hiccup never wrongly locks the dashboard.
 */
export interface OrgBillingState {
  orgStatus: OrgStatus;
  /** True when billing has put the org into a no-new-resources state. */
  readOnly: boolean;
}

// Memoized per request: the (app) layout (over-limit banner) and the dashboard
// page ("New site" gating) both need this, and without dedup each one repeated
// the whole billing derivation. cache() collapses them to a single computation
// per render (the underlying GET /v1/billing is likewise deduped in lib/api).
export const loadOrgBillingState = cache(
  async (): Promise<OrgBillingState> => {
    try {
      const plan = await api.getBilling();
      const orgStatus: OrgStatus = plan.org_status ?? "active";
      return { orgStatus, readOnly: isRestricted(orgStatus) };
    } catch (err) {
      // 404 = self-host (no billing); anything else = transient. Fail OPEN so the
      // dashboard stays usable; the server still enforces the real caps.
      void (err instanceof ApiError);
      return { orgStatus: "active", readOnly: false };
    }
  },
);

/** over_limit / past_due / suspended → new cost-creating actions are paused. */
export function isRestricted(status: OrgStatus): boolean {
  return status === "over_limit" || status === "past_due" || status === "suspended";
}
