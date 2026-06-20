"use server";

import { revalidatePath } from "next/cache";

import { api, ApiError, type Domain } from "@/lib/api";

export type AddDomainResult =
  | { ok: true; domain: Domain }
  | { ok: false; message: string };

/** A loose hostname check (the Go API is authoritative; this fails fast in UI). */
const HOSTNAME_RE =
  /^(?=.{1,253}$)(?!-)[a-z0-9-]{1,63}(?<!-)(?:\.(?!-)[a-z0-9-]{1,63}(?<!-))+$/;

/**
 * Register a custom domain for a site (POST /v1/sites/{id}/domains). The Go API
 * re-checks owner/admin, creates the Cloudflare-for-SaaS custom hostname, and
 * returns the pending row plus the DNS DCV record the user must create. The
 * hostname is globally unique → 409 if another org/site already claimed it.
 */
export async function addDomainAction(input: {
  siteId: string;
  hostname: string;
}): Promise<AddDomainResult> {
  const hostname = input.hostname.trim().toLowerCase().replace(/\.$/, "");
  if (!HOSTNAME_RE.test(hostname)) {
    return { ok: false, message: "Enter a valid domain, e.g. docs.acme.com." };
  }
  // A bare apex with no subdomain label can't get a CNAME-based DCV; nudge users.
  if (hostname.endsWith(".dropwaycontent.com")) {
    return {
      ok: false,
      message: "That's a platform domain. Add your own custom domain instead.",
    };
  }

  try {
    const domain = await api.addDomain(input.siteId, hostname);
    revalidatePath(`/sites/${input.siteId}/domains`);
    return { ok: true, domain };
  } catch (err) {
    if (err instanceof ApiError) {
      const apiMsg = (err.body as { message?: string } | null)?.message;
      if (apiMsg) return { ok: false, message: apiMsg };
      if (err.status === 409) {
        return {
          ok: false,
          message: "That domain is already in use by another site.",
        };
      }
      if (err.status === 403) {
        return {
          ok: false,
          message: "Only owners and admins can add custom domains.",
        };
      }
      if (err.status === 503) {
        return {
          ok: false,
          message: "Custom domains aren't available right now. Try again later.",
        };
      }
      return { ok: false, message: "Could not add the domain. Try again." };
    }
    return { ok: false, message: "Could not reach the API. Try again." };
  }
}

export type RemoveDomainResult = { ok: true } | { ok: false; message: string };

/**
 * Remove a custom domain (DELETE /v1/domains/{id}). The Go API re-checks owner/admin,
 * drops the global host route (so the custom host stops serving), and best-effort
 * deletes the Cloudflare custom hostname.
 */
export async function removeDomainAction(input: {
  siteId: string;
  domainId: string;
}): Promise<RemoveDomainResult> {
  try {
    await api.deleteDomain(input.domainId);
    revalidatePath(`/sites/${input.siteId}/domains`);
    return { ok: true };
  } catch (err) {
    if (err instanceof ApiError) {
      if (err.status === 403) {
        return {
          ok: false,
          message: "Only owners and admins can remove custom domains.",
        };
      }
      if (err.status === 404) {
        // Already gone, treat as success so the row clears.
        return { ok: true };
      }
      return { ok: false, message: "Could not remove the domain. Try again." };
    }
    return { ok: false, message: "Could not reach the API. Try again." };
  }
}

export type DomainStatusResult =
  | { ok: true; domain: Domain }
  | { ok: false; message: string };

/**
 * Poll a domain's verification + TLS status (GET /v1/domains/{id}/status). The
 * Go API advances the pending→verifying→verified state machine and, once
 * verified + TLS issued, writes the global host route so the custom host serves.
 */
export async function refreshDomainStatusAction(
  domainId: string,
): Promise<DomainStatusResult> {
  try {
    const domain = await api.getDomainStatus(domainId);
    return { ok: true, domain };
  } catch (err) {
    if (err instanceof ApiError) {
      if (err.status === 404) {
        return { ok: false, message: "This domain was removed." };
      }
      return { ok: false, message: "Couldn't refresh status. Try again." };
    }
    return { ok: false, message: "Could not reach the API. Try again." };
  }
}
