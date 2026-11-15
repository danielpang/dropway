"use server";

import { api, ApiError } from "@/lib/api";
import { collaborationEnabled } from "@/lib/project-room/capabilities";

/** Only metadata crosses this action. Image bytes use the existing presigned,
 * quota-checked deployment pipeline, and are never public by default. */
export async function createProjectImageAction(title: string, operationId: string) {
  if (!(await collaborationEnabled())) return { ok: false as const, message: "Projects are unavailable for this account." };
  if (!/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(operationId)) return { ok: false as const, message: "Start a new image upload." };
  const slug = `image-${operationId.toLowerCase()}`;
  const label = title.trim();
  if (!label || Buffer.byteLength(label, "utf8") > 255) return { ok: false as const, message: "Choose an image with a shorter filename." };
  try {
    const site = await api.createSite({
      slug,
      resource_kind: "image",
      title: label,
      access_mode: "org_only",
    });
    if (!site.id || site.resource_kind !== "image" || site.access_mode !== "org_only") throw new Error("Invalid image identity");
    return { ok: true as const, siteId: site.id };
  } catch (error) {
    // Creation may have committed even when its response was lost. Recover only
    // the same caller's private image in the current org, never someone else's
    // resource sharing a caller-supplied operation identifier.
    try {
      const [sites, me] = await Promise.all([api.listSites(), api.me()]);
      const existing = sites.find((site) => site.slug === slug && site.id
        && site.resource_kind === "image" && site.access_mode === "org_only"
        && site.title === label && me.user_id && site.owner_id === me.user_id
        && me.org_id && site.org_id === me.org_id);
      if (existing?.id) return { ok: true as const, siteId: existing.id };
    } catch { /* Keep the retry key; a later attempt can recover the same image. */ }
    if (error instanceof ApiError && error.asQuotaExceeded()) return { ok: false as const, message: "Your workspace has reached its resource limit. Free up space or upgrade before uploading." };
    return { ok: false as const, message: "The image could not be created. Please try again." };
  }
}
