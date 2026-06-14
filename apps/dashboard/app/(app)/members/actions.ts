"use server";

import { revalidatePath } from "next/cache";

import { api, ApiError } from "@/lib/api";

/**
 * Result of a hard-revocation ("sign out / revoke access everywhere") write.
 * `unavailable` is the graceful path for builds where the Go API's Phase-4
 * /v1/orgs/revoke-access endpoint isn't present yet (404) — the UI shows an
 * "not available on this deployment" note instead of an error.
 */
export type RevokeActionResult =
  | { ok: true; minIat: number | null }
  | { ok: false; unavailable: true }
  | { ok: false; unavailable?: false; message: string };

/**
 * Hard-revoke edge tokens for a subject via the KV denylist (the REVOCATION
 * DENYLIST CONTRACT): bumps `revoked:<kind>:<id>.min_iat` so every edge token
 * issued before now is rejected at the serving Worker and the /authz exchange.
 *
 *   - kind="org"  → org-wide kill switch: signs every viewer out of every gated
 *     site in the org, forcing re-auth. Used for incident response / tightening.
 *   - kind="user" → revoke one member's content access immediately (pairs with
 *     removing them, so they can't ride a still-valid 15m edge token).
 *
 * The Go API re-checks owner/admin (this is not the security boundary) and the
 * write is idempotent server-side (max of existing/new min_iat). A stale
 * denylist only fails CLOSED (extra re-auth), never opens access.
 */
export async function revokeAccessAction(input: {
  kind: "org" | "user";
  id: string;
}): Promise<RevokeActionResult> {
  if (!input.id) {
    return { ok: false, message: "Nothing to revoke." };
  }
  try {
    const result = await api.revokeAccess(input);
    // Membership/access changed → refresh the members + audit views.
    revalidatePath("/members");
    revalidatePath("/audit");
    return { ok: true, minIat: result.min_iat ?? null };
  } catch (err) {
    if (err instanceof ApiError) {
      // 404 → endpoint not on this build (Phase-4 revocation not wired yet).
      if (err.status === 404) return { ok: false, unavailable: true };
      const apiMsg = (err.body as { message?: string } | null)?.message;
      if (apiMsg) return { ok: false, message: apiMsg };
      if (err.status === 403) {
        return {
          ok: false,
          message: "Only owners and admins can revoke access.",
        };
      }
      return { ok: false, message: "Could not revoke access. Try again." };
    }
    return { ok: false, message: "Could not reach the API. Try again." };
  }
}
