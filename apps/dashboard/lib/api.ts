import "server-only";

import { headers } from "next/headers";

import { auth } from "@/lib/auth";
import { API_URL } from "@/lib/env";

/**
 * Typed client stub for the Go control-plane API (api.shipped.app).
 *
 * The dashboard's contract is the API, NEVER the database (architecture §8): it
 * calls the Go API for ALL business data, carrying a short-lived Better Auth
 * EdDSA JWT in the Authorization header. The Go API verifies that JWT and is the
 * authz boundary + system of record.
 *
 * This is a hand-written placeholder. Once the Go service publishes its OpenAPI
 * spec, this file is REPLACED by a generated, fully-typed client (the shapes
 * below mirror the planned resources so call sites compile against them today).
 */

// ---- Shared error envelope ------------------------------------------------

/**
 * The 402 body the API returns when a hard cap is hit (architecture §9). This
 * MUST mirror Go's `quota.ExceededError` (internal/quota/quota.go) exactly —
 * `limit` is the resource STRING and there is no top-level `error` discriminator;
 * a 402 status is itself the signal.
 */
export type QuotaResource = "sites_per_user" | "members_per_org";

export interface QuotaExceeded {
  limit: QuotaResource;
  current: number;
  max: number;
  plan_tier: "free" | "business" | "enterprise";
  next_tier?: "business" | "enterprise" | "contact_sales";
  upgrade_url?: string;
  sales_url?: string;
}

export class ApiError extends Error {
  readonly status: number;
  readonly body: unknown;
  constructor(status: number, message: string, body: unknown) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.body = body;
  }

  /**
   * Narrow to a 402 quota payload. The Go API signals a cap hit purely by the
   * 402 status (no `error` discriminator); the body is `quota.ExceededError`.
   */
  asQuotaExceeded(): QuotaExceeded | null {
    if (
      this.status === 402 &&
      typeof this.body === "object" &&
      this.body !== null &&
      typeof (this.body as { limit?: unknown }).limit === "string"
    ) {
      return this.body as QuotaExceeded;
    }
    return null;
  }
}

// ---- Resource shapes (placeholders until OpenAPI codegen) -----------------

export type AccessMode = "public" | "password" | "allowlist" | "org_only";

export interface Site {
  id: string;
  org_id: string;
  slug: string;
  access_mode: AccessMode;
  current_version_id: string | null;
  created_at: string;
}

export interface SiteVersion {
  id: string;
  site_id: string;
  version_no: number;
  status: "pending" | "ready" | "failed";
  content_hash: string;
  size: number;
  created_at: string;
}

// ---- Auth: fetch a fresh EdDSA JWT for the active session -----------------

/**
 * Mints/fetches the short-lived EdDSA JWT for the current Better Auth session.
 * The jwt() plugin exposes a `getToken` server action; we forward the request
 * cookies so it resolves the caller's session.
 */
async function bearerToken(): Promise<string | null> {
  const requestHeaders = await headers();
  const result = await auth.api.getToken({ headers: requestHeaders });
  return result?.token ?? null;
}

// ---- Core fetch wrapper ---------------------------------------------------

async function apiFetch<T>(
  path: string,
  init: RequestInit = {},
): Promise<T> {
  const token = await bearerToken();
  const res = await fetch(`${API_URL}${path}`, {
    ...init,
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...init.headers,
    },
    // Business data is per-user; never serve a shared cache.
    cache: "no-store",
  });

  if (!res.ok) {
    let body: unknown = null;
    try {
      body = await res.json();
    } catch {
      // non-JSON error body; leave as null
    }
    throw new ApiError(res.status, `API ${res.status} on ${path}`, body);
  }

  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

// ---- Typed endpoints (subset, expand as the API grows) --------------------

export const api = {
  /** List the caller's sites within the active org. */
  listSites(): Promise<Site[]> {
    return apiFetch<Site[]>("/v1/sites");
  },

  /** Create a new site (subject to the cloud quota gate → may 402). */
  createSite(input: { slug: string }): Promise<Site> {
    return apiFetch<Site>("/v1/sites", {
      method: "POST",
      body: JSON.stringify(input),
    });
  },

  /** List the versions (deploys) of a site, newest first. */
  listVersions(siteId: string): Promise<SiteVersion[]> {
    return apiFetch<SiteVersion[]>(`/v1/sites/${siteId}/versions`);
  },
};
