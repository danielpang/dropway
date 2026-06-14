import "server-only";

import { headers } from "next/headers";

import { auth } from "@/lib/auth";
import { API_URL } from "@/lib/env";
import type { components, operations } from "@/lib/api-generated/schema";

/**
 * Typed client for the Go control-plane API (api.shipped.app).
 *
 * The dashboard's contract is the API, NEVER the database (architecture §8): it
 * calls the Go API for ALL business data, carrying a short-lived Better Auth
 * EdDSA JWT in the Authorization header. The Go API verifies that JWT and is the
 * authz boundary + system of record.
 *
 * The request/response SHAPES below are derived from the generated OpenAPI types
 * (`lib/api-generated/schema.ts`, regenerate with `pnpm gen:api`). The thin
 * wrapper here adds the JWT, JSON handling, and the 402 quota narrowing the
 * dashboard's upgrade modal depends on.
 */

// ---- Resource shapes (re-exported from the generated schema) --------------

export type Me = components["schemas"]["Me"];
export type Site = components["schemas"]["Site"];
export type Version = components["schemas"]["Version"];
export type ManifestFile = components["schemas"]["ManifestFile"];
export type AccessMode = NonNullable<Site["access_mode"]>;
export type Role = NonNullable<Me["role"]>;
export type Member = components["schemas"]["Member"];
export type AllowlistEntry = components["schemas"]["AllowlistEntry"];
export type Domain = components["schemas"]["Domain"];
export type EdgeToken = components["schemas"]["EdgeToken"];

/** Successful body of `POST /v1/sites/{id}/publish` (the live URL + version). */
export type PublishResult =
  operations["publish"]["responses"]["200"]["content"]["application/json"];

/** Body the dashboard sends to `PUT /v1/sites/{id}/access`. */
export type SetAccessInput =
  operations["setSiteAccess"]["requestBody"]["content"]["application/json"];

/** Successful body of `PUT /v1/sites/{id}/access`. */
export type SetAccessResult =
  operations["setSiteAccess"]["responses"]["200"]["content"]["application/json"];

/** Successful body of `PUT /v1/orgs/allow-external-sharing` (policy + reconcile count). */
export type AllowExternalResult =
  operations["setAllowExternalSharing"]["responses"]["200"]["content"]["application/json"];

// ---- Shared error envelope ------------------------------------------------

/**
 * The 402 body the API returns when a hard cap is hit (architecture §9). This
 * mirrors Go's `quota.ExceededError` (internal/quota/quota.go) exactly — `limit`
 * is the resource STRING and there is no top-level `error` discriminator; the
 * 402 status is itself the signal. Sourced from the generated schema so it stays
 * in lockstep with the spec.
 */
export type QuotaExceeded = components["schemas"]["QuotaExceeded"];
export type QuotaResource = NonNullable<QuotaExceeded["limit"]>;

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
   * 402 status (no `error` discriminator); the body is `quota.ExceededError`,
   * recognized by its string `limit`.
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

/**
 * Like apiFetch but JWT-FREE — for the password-mode authz exchange, whose Go
 * endpoint is `security: []` (the password is the only credential and the minted
 * token's sub is anonymous). Deliberately omits the Authorization header so the
 * viewer's dashboard identity never leaks into an anonymous content grant.
 */
async function apiFetchPublic<T>(
  path: string,
  init: RequestInit = {},
): Promise<T> {
  const res = await fetch(`${API_URL}${path}`, {
    ...init,
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
      ...init.headers,
    },
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

// ---- Typed endpoints (Phase 1 + Phase 2 surface; mirrors openapi.yaml) -----

export const api = {
  /** Echo the caller's verified identity (user_id / org_id / role). */
  me(): Promise<Me> {
    return apiFetch<Me>("/v1/me");
  },

  /** List the caller org's sites. */
  async listSites(): Promise<Site[]> {
    const body = await apiFetch<{ sites?: Site[] }>("/v1/sites");
    return body.sites ?? [];
  },

  /** Get one site by id (404 → ApiError with status 404). */
  getSite(id: string): Promise<Site> {
    return apiFetch<Site>(`/v1/sites/${id}`);
  },

  /** Create a new site (subject to the cloud quota gate → may 402). */
  createSite(input: { slug: string }): Promise<Site> {
    return apiFetch<Site>("/v1/sites", {
      method: "POST",
      body: JSON.stringify(input),
    });
  },

  /**
   * Publish (or roll back to) a version: flips the site's live-version pointer.
   * Rollback is just publishing an older version_id.
   */
  publish(siteId: string, input: { version_id: string }): Promise<PublishResult> {
    return apiFetch<PublishResult>(`/v1/sites/${siteId}/publish`, {
      method: "POST",
      body: JSON.stringify(input),
    });
  },

  // ---- Phase 2: access control, sharing policy, members, domains ----------

  /** List the caller org's members (Better Auth roles, RLS/org-scoped). */
  async listMembers(): Promise<Member[]> {
    const body = await apiFetch<{ members?: Member[] }>("/v1/members");
    return body.members ?? [];
  },

  /**
   * Set a site's access mode + policy (admin/owner only → 403 otherwise). The
   * Go API hashes any password server-side and rewrites the edge RouteValue.
   */
  setSiteAccess(siteId: string, input: SetAccessInput): Promise<SetAccessResult> {
    return apiFetch<SetAccessResult>(`/v1/sites/${siteId}/access`, {
      method: "PUT",
      body: JSON.stringify(input),
    });
  },

  /** List a site's allowlist (emails + claim state). */
  async listAllowlist(siteId: string): Promise<AllowlistEntry[]> {
    const body = await apiFetch<{ allowlist?: AllowlistEntry[] }>(
      `/v1/sites/${siteId}/allowlist`,
    );
    return body.allowlist ?? [];
  },

  /**
   * Add an email to a site's allowlist (admin/owner only). is_external is set
   * server-side; an external grant under allow_external_sharing=false → 403.
   */
  addAllowlistEntry(siteId: string, email: string): Promise<AllowlistEntry> {
    return apiFetch<AllowlistEntry>(`/v1/sites/${siteId}/allowlist`, {
      method: "POST",
      body: JSON.stringify({ email }),
    });
  },

  /** Remove an email from a site's allowlist (admin/owner only). */
  removeAllowlistEntry(siteId: string, email: string): Promise<{ removed?: string }> {
    return apiFetch<{ removed?: string }>(`/v1/sites/${siteId}/allowlist`, {
      method: "DELETE",
      body: JSON.stringify({ email }),
    });
  },

  /** List a site's custom domains. */
  async listDomains(siteId: string): Promise<Domain[]> {
    const body = await apiFetch<{ domains?: Domain[] }>(
      `/v1/sites/${siteId}/domains`,
    );
    return body.domains ?? [];
  },

  /**
   * Register a custom domain for a site (admin/owner only). Returns the pending
   * row + the DNS DCV record the user must create. 409 if the host is taken.
   */
  addDomain(siteId: string, hostname: string): Promise<Domain> {
    return apiFetch<Domain>(`/v1/sites/${siteId}/domains`, {
      method: "POST",
      body: JSON.stringify({ hostname }),
    });
  },

  /** Poll a custom domain's verification + TLS status (advances the state machine). */
  getDomainStatus(domainId: string): Promise<Domain> {
    return apiFetch<Domain>(`/v1/domains/${domainId}/status`);
  },

  /**
   * Toggle the org allow_external_sharing policy (owner/admin only → 403). When
   * disabling, the API reconciles (downgrades public sites + revokes external
   * grants) and returns the count of downgraded sites.
   */
  setAllowExternalSharing(enabled: boolean): Promise<AllowExternalResult> {
    return apiFetch<AllowExternalResult>("/v1/orgs/allow-external-sharing", {
      method: "PUT",
      body: JSON.stringify({ enabled }),
    });
  },

  /**
   * Mint a host-scoped edge token for an org_only/allowlist site (the viewer's
   * Better Auth JWT authorizes). 403 → not permitted; 400 → password-mode host.
   */
  authzMint(input: { host: string; next?: string }): Promise<EdgeToken> {
    return apiFetch<EdgeToken>("/v1/authz/mint", {
      method: "POST",
      body: JSON.stringify(input),
    });
  },

  /**
   * Mint an anonymous edge token for a password-protected site. JWT-FREE — the
   * password is the only credential. 401 → wrong password / unknown host.
   */
  authzPassword(input: { host: string; password: string }): Promise<EdgeToken> {
    return apiFetchPublic<EdgeToken>("/v1/authz/password", {
      method: "POST",
      body: JSON.stringify(input),
    });
  },
};
