"use server";

import { revalidatePath } from "next/cache";

import {
  api,
  ApiError,
  type ManifestFile,
  type PublishResult,
  type QuotaExceeded,
  type Version,
} from "@/lib/api";

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

// ---- Folder drag-and-drop deploy (browser hashes + uploads; these wrap the two
// JSON round-trips so the JWT stays server-side; the blob PUTs happen client-side
// directly to object storage). ----

/** A friendly message for a deploy ApiError, with sensible status fallbacks. */
function deployErrorMessage(err: unknown, fallback: string): string {
  if (err instanceof ApiError) {
    return (
      (err.body as { message?: string } | null)?.message ??
      (err.status === 404
        ? "This site no longer exists."
        : err.status === 403
          ? "You don't have permission to deploy to this site."
          : err.status === 400
            ? "The deploy was rejected (a file changed mid-upload). Try again."
            : fallback)
    );
  }
  return "Could not reach the API. Try again.";
}

export type PrepareDeployActionResult =
  | { ok: true; missing: string[]; uploads: Record<string, string> }
  | { ok: false; message: string };

/**
 * Prepare a deployment (POST /v1/sites/{id}/deployments/prepare). Takes the
 * browser-computed manifest and returns which blobs are missing + a presigned PUT
 * URL for each, which the browser then uploads directly to object storage. The
 * JWT never leaves the server; only the manifest of hashes crosses the boundary.
 */
export async function prepareDeploymentAction(input: {
  siteId: string;
  manifest: ManifestFile[];
}): Promise<PrepareDeployActionResult> {
  if (!input.manifest.length) {
    return { ok: false, message: "Nothing to deploy — the folder has no files." };
  }
  try {
    const res = await api.prepareDeployment(input.siteId, {
      manifest: input.manifest,
    });
    return { ok: true, missing: res.missing ?? [], uploads: res.uploads ?? {} };
  } catch (err) {
    return { ok: false, message: deployErrorMessage(err, "Could not start the deploy. Try again.") };
  }
}

export type FinalizeDeployActionResult =
  | { ok: true; version: Version }
  | { ok: false; message: string; quota?: QuotaExceeded };

/**
 * Finalize a deployment (POST /v1/sites/{id}/deployments) once every missing
 * blob is uploaded. The API re-hashes the stored blobs + re-derives the digest,
 * then creates the immutable version. A storage-cap hit surfaces as 402 (cloud
 * build) → returned as `quota` so the UI can show the upgrade affordance.
 */
export async function finalizeDeploymentAction(input: {
  siteId: string;
  manifest: ManifestFile[];
  digest: string;
}): Promise<FinalizeDeployActionResult> {
  try {
    const version = await api.finalizeDeployment(input.siteId, {
      manifest: input.manifest,
      digest: input.digest,
    });
    return { ok: true, version };
  } catch (err) {
    if (err instanceof ApiError) {
      const quota = err.asQuotaExceeded();
      if (quota) {
        return {
          ok: false,
          message: "This deploy would exceed your storage limit.",
          quota,
        };
      }
    }
    return { ok: false, message: deployErrorMessage(err, "Could not finalize the deploy. Try again.") };
  }
}
