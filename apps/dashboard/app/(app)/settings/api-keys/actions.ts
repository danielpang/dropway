"use server";

import { revalidatePath } from "next/cache";

import { api, ApiError, type ApiKey, type ApiKeyCreated } from "@/lib/api";

/** Map an API error to a friendly message, preferring the server's own detail. */
function messageFor(err: unknown, fallback: string, forbidden: string): string {
  if (err instanceof ApiError) {
    const apiMsg = (err.body as { message?: string } | null)?.message;
    if (apiMsg) return apiMsg;
    if (err.status === 403) return forbidden;
    if (err.status === 404) return "That key no longer exists.";
    return fallback;
  }
  return "Could not reach the API. Try again.";
}

export type CreateApiKeyResult =
  | { ok: true; key: ApiKeyCreated }
  | { ok: false; message: string };

/**
 * Mint an org-scoped API key. The Go API re-checks owner/admin (and refuses
 * API-key callers) and returns the full secret exactly once — this is the only
 * time the plaintext exists, so the caller reveals it immediately and never again.
 */
export async function createApiKeyAction(input: {
  name: string;
}): Promise<CreateApiKeyResult> {
  const name = input.name.trim();
  if (!name) return { ok: false, message: "Give the key a name." };
  try {
    const key = await api.createApiKey(name);
    revalidatePath("/settings/api-keys");
    return { ok: true, key };
  } catch (err) {
    return {
      ok: false,
      message: messageFor(
        err,
        "Could not create the key. Try again.",
        "Only owners and admins can create API keys.",
      ),
    };
  }
}

export type RevokeApiKeyResult =
  | { ok: true; key: ApiKey }
  | { ok: false; message: string };

/**
 * Revoke a key by id. Idempotent and immediate — the very next request presenting
 * the key is rejected. Owner/admin only (the Go API re-checks and 403s otherwise).
 */
export async function revokeApiKeyAction(input: {
  id: string;
}): Promise<RevokeApiKeyResult> {
  try {
    const key = await api.revokeApiKey(input.id);
    revalidatePath("/settings/api-keys");
    return { ok: true, key };
  } catch (err) {
    return {
      ok: false,
      message: messageFor(
        err,
        "Could not revoke the key. Try again.",
        "Only owners and admins can revoke API keys.",
      ),
    };
  }
}

export type ApiKeysEnabledResult =
  | { ok: true; enabled: boolean }
  | { ok: false; message: string };

/**
 * Flip the org-wide API-keys kill switch. The Go API re-checks owner/admin. The
 * key auth boundary re-checks the flag per request, so disabling 401s every org
 * key immediately; existing keys are kept (not deleted) and management still works.
 */
export async function setApiKeysEnabledAction(input: {
  enabled: boolean;
}): Promise<ApiKeysEnabledResult> {
  try {
    const result = await api.setApiKeysEnabled(input.enabled);
    revalidatePath("/settings/api-keys");
    revalidatePath("/settings");
    return { ok: true, enabled: result.api_keys_enabled };
  } catch (err) {
    return {
      ok: false,
      message: messageFor(
        err,
        "Could not update the API-keys setting. Try again.",
        "Only owners and admins can change API-key access.",
      ),
    };
  }
}
