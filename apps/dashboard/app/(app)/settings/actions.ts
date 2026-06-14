"use server";

import { revalidatePath } from "next/cache";

import { api, ApiError, type AllowExternalResult } from "@/lib/api";

export type AllowExternalActionResult =
  | { ok: true; result: AllowExternalResult }
  | { ok: false; message: string };

/**
 * Toggle the org-wide `allow_external_sharing` policy (architecture §5.4). The
 * Go API re-checks owner/admin and, when DISABLING, reconciles: it downgrades
 * public sites to org_only, revokes external-email grants, and rewrites the edge
 * routes — returning the count of downgraded sites so the UI can confirm the
 * blast radius. Enabling only widens what's permitted (no reconcile).
 */
export async function setAllowExternalSharingAction(input: {
  enabled: boolean;
}): Promise<AllowExternalActionResult> {
  try {
    const result = await api.setAllowExternalSharing(input.enabled);
    revalidatePath("/settings");
    revalidatePath("/dashboard");
    return { ok: true, result };
  } catch (err) {
    if (err instanceof ApiError) {
      const apiMsg = (err.body as { message?: string } | null)?.message;
      if (apiMsg) return { ok: false, message: apiMsg };
      if (err.status === 403) {
        return {
          ok: false,
          message: "Only owners and admins can change this policy.",
        };
      }
      return { ok: false, message: "Could not update the policy. Try again." };
    }
    return { ok: false, message: "Could not reach the API. Try again." };
  }
}
