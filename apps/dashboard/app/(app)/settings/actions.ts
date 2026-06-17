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

export type McpActionResult =
  | { ok: true; mcpEnabled: boolean }
  | { ok: false; message: string };

/**
 * Toggle whether the Dropway MCP server may serve this org (owner/admin only → the
 * Go API re-checks the role and 403s otherwise). The MCP resource server re-checks
 * the flag per request, so disabling cuts off MCP access immediately even for
 * already-issued OAuth tokens.
 */
export async function setMcpEnabledAction(input: {
  enabled: boolean;
}): Promise<McpActionResult> {
  try {
    const result = await api.setMcpEnabled(input.enabled);
    revalidatePath("/settings");
    return { ok: true, mcpEnabled: result.mcp_enabled };
  } catch (err) {
    if (err instanceof ApiError) {
      const apiMsg = (err.body as { message?: string } | null)?.message;
      if (apiMsg) return { ok: false, message: apiMsg };
      if (err.status === 403) {
        return {
          ok: false,
          message: "Only owners and admins can change MCP access.",
        };
      }
      return { ok: false, message: "Could not update MCP access. Try again." };
    }
    return { ok: false, message: "Could not reach the API. Try again." };
  }
}
