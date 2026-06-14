"use server";

import { revalidatePath } from "next/cache";

import { api, ApiError, type PublishResult } from "@/lib/api";

/**
 * Result envelope for the publish/rollback action. Like create-site, server
 * actions can't throw rich typed errors across the boundary, so we return a
 * discriminated union the client form can branch on.
 */
export type PublishActionResult =
  | { ok: true; result: PublishResult }
  | { ok: false; message: string };

/**
 * Publish (or roll back to) a version via POST /v1/sites/{id}/publish. Rollback
 * is just publishing an older version_id — the Go API flips the live pointer and
 * rewrites the edge routing projection. Re-renders the site page on success.
 */
export async function publishVersionAction(input: {
  siteId: string;
  versionId: string;
}): Promise<PublishActionResult> {
  const versionId = input.versionId.trim();
  if (!versionId) {
    return { ok: false, message: "Enter a version id to publish." };
  }

  try {
    const result = await api.publish(input.siteId, { version_id: versionId });
    revalidatePath(`/sites/${input.siteId}`);
    revalidatePath("/dashboard");
    return { ok: true, result };
  } catch (err) {
    if (err instanceof ApiError) {
      const message =
        (err.body as { message?: string } | null)?.message ??
        (err.status === 404
          ? "That version doesn't exist for this site."
          : err.status === 400
            ? "That version can't be published."
            : "Could not publish. Try again.");
      return { ok: false, message };
    }
    return { ok: false, message: "Could not reach the API. Try again." };
  }
}
