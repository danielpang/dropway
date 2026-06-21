"use server";

import { revalidatePath } from "next/cache";

import { captureSiteCreated } from "@/lib/analytics-server";
import { api, ApiError, type QuotaExceeded, type Site } from "@/lib/api";
import { getCurrentSession } from "@/lib/session";

/**
 * Result envelope for the create-site action. The dashboard's client dialog
 * needs to distinguish three outcomes: success, a normal validation error
 * (e.g. reserved/duplicate slug → 400), and a quota cap (402) that should open
 * the upgrade modal with the quota body. Server actions can't throw rich typed
 * errors across the boundary, so we return a discriminated union.
 */
export type CreateSiteResult =
  | { ok: true; site: Site }
  | { ok: false; kind: "quota"; quota: QuotaExceeded }
  | { ok: false; kind: "error"; message: string };

/**
 * Create a site via the Go API (POST /v1/sites), carrying the caller's EdDSA
 * JWT. On the cloud build a per-user cap returns 402 with the quota body, which
 * we surface as `kind: "quota"` so the UI opens the upgrade modal.
 */
export async function createSiteAction(input: {
  slug: string;
}): Promise<CreateSiteResult> {
  const slug = input.slug.trim();
  if (!slug) {
    return { ok: false, kind: "error", message: "Enter a slug for the site." };
  }

  try {
    const site = await api.createSite({ slug });
    // Refresh the server-rendered sites list.
    revalidatePath("/dashboard");
    await recordSiteCreated(site, slug);
    return { ok: true, site };
  } catch (err) {
    if (err instanceof ApiError) {
      const quota = err.asQuotaExceeded();
      if (quota) return { ok: false, kind: "quota", quota };

      // Surface the API's machine message when present (e.g. reserved slug).
      const message =
        (err.body as { message?: string } | null)?.message ??
        (err.status === 400
          ? "That slug is unavailable. Try another."
          : "Could not create the site. Try again.");
      return { ok: false, kind: "error", message };
    }
    return {
      ok: false,
      kind: "error",
      message: "Could not reach the API. Try again.",
    };
  }
}

/** Best-effort `site_created` analytics, attributed to the acting user + active
 * org. Never throws into the action. */
async function recordSiteCreated(site: Site, slug: string): Promise<void> {
  try {
    const session = await getCurrentSession();
    const userId = (session?.user as { id?: string } | undefined)?.id;
    const organization =
      (session?.session as { activeOrganizationId?: string | null } | undefined)
        ?.activeOrganizationId ?? null;
    if (userId && organization) {
      await captureSiteCreated({
        userId,
        organization,
        siteId: site.id,
        slug: site.slug ?? slug,
      });
    }
  } catch {
    // analytics is non-fatal
  }
}
