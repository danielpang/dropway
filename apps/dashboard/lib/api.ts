import "server-only";

import { cache } from "react";
import { headers } from "next/headers";
import { isAPIError } from "better-auth/api";

import { auth } from "@/lib/auth";
import { API_URL } from "@/lib/env";
import { getCurrentSession } from "@/lib/session";
import { TokenCache, tokenCacheKey } from "@/lib/token-cache";
import type { components, operations } from "@/lib/api-generated/schema";

/**
 * Typed client for the Go control-plane API (api.dropway.dev).
 *
 * The dashboard's contract is the API, NEVER the database: it
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
/** One post in the org feed: a Site plus vote score / the caller's vote / comment count. */
export type FeedItem = components["schemas"]["FeedItem"];
export type Version = components["schemas"]["Version"];
/** One immutable deploy in a site's history (the rollback picker rows). */
export type SiteVersion = components["schemas"]["SiteVersion"];
export type ManifestFile = components["schemas"]["ManifestFile"];
export type AccessMode = NonNullable<Site["access_mode"]>;
export type Role = NonNullable<Me["role"]>;
export type Member = components["schemas"]["Member"];
/** One user's logical (non-deduplicated) storage total in the org, in bytes. */
export type UserStorage = components["schemas"]["UserStorage"];
export type AllowlistEntry = components["schemas"]["AllowlistEntry"];
/** One org-internal comment on a site (with @mentions). */
export type SiteComment = components["schemas"]["SiteComment"];
export type Domain = components["schemas"]["Domain"];
export type EdgeToken = components["schemas"]["EdgeToken"];

/** One entry in the OpenRouter model catalog the AI builder's picker renders. */
export type AiModel = {
  id: string;
  name?: string;
  description?: string;
  context_length?: number;
  pricing?: { prompt?: string; completion?: string };
};

// ---- Org-wide skill sharing shapes -----------------------------------------

/** An org-shared Claude skill (SKILL.md + supporting files, latest-only versions). */
export type Skill = components["schemas"]["Skill"];
/** One folder membership as seen from a skill (with its preset flag). */
export type SkillFolderRef = components["schemas"]["SkillFolderRef"];
/** An admin-curated skill folder (engineering/product/marketing by default). */
export type SkillFolder = components["schemas"]["SkillFolder"];
/** One skill's files inline (utf8 / base64), or a truncated stub in bulk downloads. */
export type SkillDownload = components["schemas"]["SkillDownload"];
/** Successful body of `POST /v1/skills/{id}/uploads` (finalize = publish). */
export type SkillUploadResult =
  operations["finalizeSkillUpload"]["responses"]["201"]["content"]["application/json"];
/** Successful body of `GET /v1/skill-folders/{id}/download`. */
export type SkillFolderDownload =
  operations["downloadSkillFolder"]["responses"]["200"]["content"]["application/json"];

// ---- Phase 4: audit log + hard revocation --------------------------------
//
// NOTE: as of this writing the Go API's /v1/audit and /v1/orgs/revoke-access
// endpoints are NOT YET in services/api/openapi/openapi.yaml (the Go agent's
// Phase-4 audit + denylist work). So these shapes are hand-written to the
// REVOCATION DENYLIST CONTRACT / app.audit_log table and are intentionally
// permissive (every field optional) so the UI stays forward-compatible with
// the spec the Go agent ships.
//
// TODO(phase4): once /v1/audit and /v1/orgs/revoke-access land in openapi.yaml,
// run `pnpm gen:api` and replace these with
//   components["schemas"]["AuditEvent"] etc., the runtime methods below already
// degrade gracefully (404 → "not supported on this build") so no UI changes are
// needed when that happens.

/** One row of app.audit_log (org_id, actor_user, actor_token, action, target, metadata, ip, created_at). */
export interface AuditEvent {
  /** Stable row id (for React keys / pagination cursors). */
  id?: string;
  /** RFC3339 timestamp the event was recorded. */
  created_at?: string;
  /** Dotted action verb, e.g. "member.removed", "site.unshared", "org.external_sharing.disabled". */
  action?: string;
  /** The acting user id (null for token/service actors). */
  actor_user?: string | null;
  /** Human label for the actor when the API can resolve it (email / name). */
  actor_label?: string | null;
  /** The acting deploy/edge token id, when the actor was a token rather than a user. */
  actor_token?: string | null;
  /** The thing acted on (a site id/slug, member id, email, org id …). */
  target?: string | null;
  /** Source IP of the request that produced the event. */
  ip?: string | null;
  /** Free-form structured context (jsonb). */
  metadata?: Record<string, unknown> | null;
  /** Correlated request id (Phase-4 tracing), when present. */
  request_id?: string | null;
}

/** A page of audit events (cursor + offset friendly). */
export interface AuditPage {
  events: AuditEvent[];
  /** Total matching rows, when the API reports it (drives the page count). */
  total?: number;
  /** Opaque cursor for the next page, when the API is cursor-based. */
  next_cursor?: string | null;
}

/** Result of a "sign out / revoke access everywhere" write (denylist min_iat bump). */
export interface RevokeResult {
  /** Echoes the subject kind/id that was revoked. */
  kind?: "user" | "site" | "org";
  id?: string;
  /** The new denylist floor (unix seconds): tokens issued before this are dead. */
  min_iat?: number;
}

/** Successful body of `POST /v1/sites/{id}/publish` (the live URL + version). */
export type PublishResult =
  operations["publish"]["responses"]["200"]["content"]["application/json"];

/** Body of `POST /v1/sites/{id}/deployments/prepare` (the file manifest). */
export type PrepareDeploymentInput =
  operations["prepareDeployment"]["requestBody"]["content"]["application/json"];

/** Successful prepare body: blobs the org lacks + a presigned PUT URL per sha256. */
export type PrepareDeploymentResult =
  operations["prepareDeployment"]["responses"]["200"]["content"]["application/json"];

/** Body of `POST /v1/sites/{id}/deployments` (finalize: full manifest + digest). */
export type FinalizeDeploymentInput =
  operations["finalizeDeployment"]["requestBody"]["content"]["application/json"];

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
 * The 402 body the API returns when a hard cap is hit. This
 * mirrors Go's `quota.ExceededError` (internal/quota/quota.go) exactly, `limit`
 * is the resource STRING and there is no top-level `error` discriminator; the
 * 402 status is itself the signal. Sourced from the generated schema so it stays
 * in lockstep with the spec.
 */
export type QuotaExceeded = components["schemas"]["QuotaExceeded"];
export type QuotaResource = NonNullable<QuotaExceeded["limit"]>;

// ---- Billing shapes (CLOUD-ONLY surface) --------------------------
//
// These mirror the [CLOUD-ONLY] /v1/billing/* endpoints. On the OSS/self-host
// build those routes don't exist (the API returns 404), the dashboard treats a
// 404 here as "no billing / unlimited" and simply hides the upgrade affordances.

/** The org's authoritative plan (GET /v1/billing). plan_tier comes from app.org_meta. */
export type BillingPlan = components["schemas"]["BillingPlan"];
/** Paid-tier ladder as the API spells it (free → business → enterprise). */
export type PlanTier = NonNullable<BillingPlan["plan_tier"]>;
/** Derived account state mirrored to the edge (drives the over-limit banner). */
export type OrgStatus = NonNullable<BillingPlan["org_status"]>;
/** The tier a Checkout session can target (the self-serve, non-contact-sales tiers). */
export type CheckoutTier =
  operations["createCheckout"]["requestBody"]["content"]["application/json"]["target_tier"];

/** Successful body of `POST /v1/billing/checkout` (Stripe-hosted URL to redirect to). */
export type CheckoutResult =
  operations["createCheckout"]["responses"]["200"]["content"]["application/json"];

/** Successful body of `POST /v1/billing/portal` (Stripe Billing Portal URL). */
export type PortalResult =
  operations["createPortal"]["responses"]["200"]["content"]["application/json"];

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

/** Mint a fresh EdDSA JWT for the caller's session (the costly path: a jwks
 * read + private-key decrypt + sign inside Better Auth). Forwards the request
 * cookies so the jwt() plugin resolves the caller's session.
 *
 * Better Auth throws an APIError("Unauthorized") when the session expired
 * between the page's session check and this mint. That is a normal signed-out
 * state, not a server fault: swallow it to null. apiFetch turns the null into a
 * local ApiError(401, {error:"reauth_required"}) — the same shape callers get
 * from a real API 401 — WITHOUT sending an unauthenticated request first. */
async function mintBearerToken(): Promise<string | null> {
  const requestHeaders = await headers();
  try {
    const result = await auth.api.getToken({ headers: requestHeaders });
    return result?.token ?? null;
  } catch (err) {
    if (isAPIError(err)) return null;
    throw err;
  }
}

/**
 * Cross-request reuse of recently-minted tokens, scoped to one server instance.
 * Together with the per-request `cache()` below, this means a burst of page
 * loads by the same user shares a single mint for the cache TTL instead of
 * re-signing on every navigation. See lib/token-cache.ts for the safety
 * argument (TTL ≪ token expiry; keyed by session + active org).
 */
const tokenCache = new TokenCache();

/**
 * Per-request cell holding the in-flight/resolved token promise for THIS request.
 * `cache()` yields one cell per request (and is a plain passthrough in unit
 * tests, so each call there gets a fresh cell).
 *
 * Holding the PROMISE — not the resolved value — preserves the concurrent
 * fan-out dedup the old `cache(async …)` memo gave (a page hitting sites +
 * billing + org mints once, the rest await the same promise). But unlike that
 * memo, the cell is MUTABLE: after a 401 recovery, refreshBearerToken republishes
 * a fresh promise here, so later apiFetch calls in the same render pick up the
 * new token instead of the stale one the first resolve pinned for the whole
 * request (which used to make every endpoint re-mint on a mid-render staleness).
 */
const requestToken = cache((): { promise?: Promise<string | null> } => ({}));

/**
 * The short-lived EdDSA JWT for the current Better Auth session.
 *
 * Two layers of reuse, both preserving the exact same token semantics:
 *  - the per-request `requestToken` cell dedupes the mint/look-up across a
 *    single render's fan-out (and lets a mid-render refresh be seen — above).
 *  - `tokenCache` reuses a still-valid token ACROSS requests for a short window,
 *    avoiding a jwks read + decrypt + sign on every page load. Keyed by session
 *    id + active org so a different user — or an org switch — always re-mints.
 */
function bearerToken(): Promise<string | null> {
  const cell = requestToken();
  if (!cell.promise) cell.promise = resolveBearerToken();
  return cell.promise;
}

/**
 * Resolve a token from the cross-request cache or a fresh mint. Falls back to an
 * uncached mint when there's no resolvable session id to key on.
 */
async function resolveBearerToken(): Promise<string | null> {
  const key = await currentTokenKey();

  // No resolvable session id → can't form a safe per-user key; mint directly.
  if (!key) return mintBearerToken();

  const cached = tokenCache.get(key);
  if (cached) return cached;

  const minted = await mintBearerToken();
  if (minted) tokenCache.set(key, minted);
  return minted;
}

/** The current session's token-cache key, or null when there's no session id to
 * key on. Shared by the cached read path and the 401-recovery path so both
 * always address the same entry. */
async function currentTokenKey(): Promise<string | null> {
  const session = await getCurrentSession();
  const sessionId =
    (session?.session as { id?: string } | undefined)?.id ?? null;
  if (!sessionId) return null;
  const activeOrgId =
    (session?.session as { activeOrganizationId?: string | null } | undefined)
      ?.activeOrganizationId ?? null;
  return tokenCacheKey(sessionId, activeOrgId);
}

/**
 * Drop the cross-request cache entry for the current session and mint fresh.
 * The 401-recovery path in apiFetch calls this when the Go API rejects a token
 * we sent: the usual cause is a cached token whose context went stale (org
 * switch settled elsewhere, a hard revocation, a key mishap), and one fresh
 * mint either heals it or proves the session itself is dead. Deliberately NOT
 * request-memoized — its whole point is to bypass the caches once.
 */
async function refreshBearerToken(): Promise<string | null> {
  const key = await currentTokenKey();
  if (key) tokenCache.delete(key);
  const minted = await mintBearerToken();
  if (minted && key) tokenCache.set(key, minted);
  // Publish to the per-request cell so later bearerToken() calls in this render
  // see the fresh token rather than the stale one the first resolve pinned.
  requestToken().promise = Promise.resolve(minted);
  return minted;
}

/**
 * The current session's bearer token, for callers that talk to the Go API
 * OUTSIDE this typed client — notably the AI builder SSE proxy route handler,
 * which streams `text/event-stream` and so can't use apiFetch's JSON path. It
 * reuses the same mint + cache, so no extra signing cost.
 */
export async function mintApiToken(): Promise<string | null> {
  return bearerToken();
}

// ---- Core fetch wrapper ---------------------------------------------------

async function apiFetch<T>(
  path: string,
  init: RequestInit = {},
): Promise<T> {
  const token = await bearerToken();

  // No mintable token = no session. Fail here with the same ApiError(401) shape
  // callers already handle, instead of sending the request UNAUTHENTICATED and
  // paying a network round trip for a guaranteed 401 (which also showed up in
  // error tracking as unexplained "APIError: Unauthorized" noise).
  if (!token) {
    throw new ApiError(401, `API 401 on ${path}`, {
      error: "reauth_required",
      message: "no valid session; sign in again",
    });
  }

  let res = await doApiFetch(path, init, token);

  // One bounded recovery: the API rejected a token we believed valid. If it came
  // from the cross-request cache it may be stale relative to the server's view
  // (org switch settled elsewhere, hard revocation, key mishap) — drop the cache
  // entry, mint fresh, retry ONCE. A fresh mint that still 401s (or a dead
  // session, mint → null) falls through to the normal ApiError below.
  if (res.status === 401) {
    const fresh = await refreshBearerToken();
    if (fresh && fresh !== token) {
      res = await doApiFetch(path, init, fresh);
    }
  }

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

function doApiFetch(
  path: string,
  init: RequestInit,
  token: string,
): Promise<Response> {
  return fetch(`${API_URL}${path}`, {
    ...init,
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
      Authorization: `Bearer ${token}`,
      ...init.headers,
    },
    // Business data is per-user; never serve a shared cache.
    cache: "no-store",
  });
}

/**
 * Like apiFetch but JWT-FREE, for the password-mode authz exchange, whose Go
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

/**
 * Per-request memoized GET. React `cache()` dedupes identical reads within a
 * single server render, so each distinct endpoint is hit AT MOST ONCE per
 * request even when several components ask for it independently — e.g. the (app)
 * layout and the dashboard page both reading `/v1/billing`, or a site page whose
 * `generateMetadata` and body both read `/v1/sites/{id}`. Previously each of
 * those repeated the full API round-trip (and its JWT mint); now the second
 * caller awaits the same in-flight promise.
 *
 * Only safe for idempotent reads — all GET endpoints route through here, while
 * writes (POST/PUT/PATCH/DELETE) keep calling `apiFetch` directly so they are
 * never collapsed. The memo lives only for the current request (cache() is
 * request-scoped), so a later navigation always re-reads fresh data.
 */
const apiGet = cache((path: string): Promise<unknown> => apiFetch(path));

// ---- Typed endpoints (Phase 1 + Phase 2 surface; mirrors openapi.yaml) -----

export const api = {
  /** Echo the caller's verified identity (user_id / org_id / role). */
  me(): Promise<Me> {
    return apiGet("/v1/me") as Promise<Me>;
  },

  /** List the caller org's sites. */
  async listSites(): Promise<Site[]> {
    const body = (await apiGet("/v1/sites")) as { sites?: Site[] };
    return body.sites ?? [];
  },

  /** Get one site by id (404 → ApiError with status 404). */
  getSite(id: string): Promise<Site> {
    return apiGet(`/v1/sites/${id}`) as Promise<Site>;
  },

  /**
   * The OpenRouter model catalog for the AI builder's model picker, plus the
   * server's default model id. Includes context window + per-token pricing so the
   * picker can show a context badge and a cost tier. Returns an empty catalog
   * when the builder is not configured (503), so callers degrade gracefully.
   */
  async aiModels(): Promise<{ models: AiModel[]; default: string }> {
    try {
      const body = (await apiGet("/v1/ai/models")) as {
        models?: AiModel[];
        default?: string;
      };
      return { models: body.models ?? [], default: body.default ?? "" };
    } catch {
      return { models: [], default: "" };
    }
  },

  /**
   * The org feed: every site teammates have shared (feed-visible, not private),
   * newest first. Any member may read it; it's the cross-user discovery surface
   * that complements the per-user site list. Private sites are filtered server-side.
   */
  async listFeed(): Promise<FeedItem[]> {
    const body = (await apiGet("/v1/feed")) as { posts?: FeedItem[] };
    return body.posts ?? [];
  },

  /**
   * Cast the caller's vote on a feed post (site or skill): value 1 (up), -1 (down),
   * or 0 to clear. Returns the post's new net score and the caller's resulting vote.
   * The endpoint is keyed by kind — sites and skills each have their own route.
   */
  setPostVote(
    kind: "site" | "skill",
    id: string,
    value: -1 | 0 | 1,
  ): Promise<{ id?: string; score?: number; my_vote?: number }> {
    const base = kind === "skill" ? "skills" : "sites";
    return apiFetch<{ id?: string; score?: number; my_vote?: number }>(
      `/v1/${base}/${id}/vote`,
      { method: "PUT", body: JSON.stringify({ value }) },
    );
  },

  /**
   * Share a site to the org feed (visible=true) or make it private (visible=false).
   * The site's owner may toggle their own site; org admins/owners may toggle any.
   * 403 otherwise. Feed visibility is orthogonal to access mode — nothing changes
   * at the edge.
   */
  setSiteFeedVisibility(
    siteId: string,
    visible: boolean,
  ): Promise<{ site_id?: string; feed_visible?: boolean }> {
    return apiFetch<{ site_id?: string; feed_visible?: boolean }>(
      `/v1/sites/${siteId}/feed`,
      { method: "PUT", body: JSON.stringify({ visible }) },
    );
  },

  /**
   * Set a site's feed title + description (owner or admin → 403 otherwise). Empty
   * strings clear the corresponding field.
   */
  setSiteFeedMeta(
    siteId: string,
    input: { title: string; description: string },
  ): Promise<{ site_id?: string; title?: string; description?: string }> {
    return apiFetch<{ site_id?: string; title?: string; description?: string }>(
      `/v1/sites/${siteId}/feed-meta`,
      { method: "PUT", body: JSON.stringify(input) },
    );
  },

  /** A site's comment thread, oldest first (any org member). */
  async listComments(siteId: string): Promise<SiteComment[]> {
    return this.listPostComments("site", siteId);
  },

  /**
   * Post a comment to a site, optionally tagging teammates by user id. Any org
   * member may comment; mentioned ids that aren't org members are dropped server-side.
   */
  addComment(
    siteId: string,
    input: { body: string; mentioned_user_ids?: string[] },
  ): Promise<SiteComment> {
    return this.addPostComment("site", siteId, input);
  },

  /**
   * A feed post's comment thread (site or skill), oldest first. Keyed by kind —
   * sites and skills each have their own comments route.
   */
  async listPostComments(kind: "site" | "skill", id: string): Promise<SiteComment[]> {
    const base = kind === "skill" ? "skills" : "sites";
    const body = (await apiGet(`/v1/${base}/${id}/comments`)) as {
      comments?: SiteComment[];
    };
    return body.comments ?? [];
  },

  /** Post a comment to a feed post (site or skill), optionally tagging teammates. */
  addPostComment(
    kind: "site" | "skill",
    id: string,
    input: { body: string; mentioned_user_ids?: string[] },
  ): Promise<SiteComment> {
    const base = kind === "skill" ? "skills" : "sites";
    return apiFetch<SiteComment>(`/v1/${base}/${id}/comments`, {
      method: "POST",
      body: JSON.stringify(input),
    });
  },

  /**
   * Set a feed post's title + description (site or skill; owner or admin → 403
   * otherwise). Empty strings clear the corresponding field. For a skill this edits
   * the skill's own title/description.
   */
  setPostFeedMeta(
    kind: "site" | "skill",
    id: string,
    input: { title: string; description: string },
  ): Promise<{ id?: string; title?: string; description?: string }> {
    const base = kind === "skill" ? "skills" : "sites";
    return apiFetch<{ id?: string; title?: string; description?: string }>(
      `/v1/${base}/${id}/feed-meta`,
      { method: "PUT", body: JSON.stringify(input) },
    );
  },

  /**
   * Share a skill to the org feed (visible=true) or make it private (false). The
   * skill's owner may toggle their own; org admins/owners may toggle any. 403 else.
   */
  setSkillFeedVisibility(
    skillId: string,
    visible: boolean,
  ): Promise<{ id?: string; feed_visible?: boolean }> {
    return apiFetch<{ id?: string; feed_visible?: boolean }>(
      `/v1/skills/${skillId}/feed`,
      { method: "PUT", body: JSON.stringify({ visible }) },
    );
  },

  /** A site's deploy history, newest first (each flagged is_current). */
  async listVersions(siteId: string): Promise<SiteVersion[]> {
    const body = (await apiGet(`/v1/sites/${siteId}/versions`)) as {
      versions?: SiteVersion[];
    };
    return body.versions ?? [];
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

  /**
   * Prepare a deployment: send the file manifest (path/sha256/size/content_type),
   * get back the blobs the org doesn't already have plus a presigned PUT URL for
   * each. The browser uploads those blobs DIRECTLY to object storage, the bytes
   * never pass through this API (only the manifest of hashes does).
   */
  prepareDeployment(
    siteId: string,
    input: PrepareDeploymentInput,
  ): Promise<PrepareDeploymentResult> {
    return apiFetch<PrepareDeploymentResult>(
      `/v1/sites/${siteId}/deployments/prepare`,
      { method: "POST", body: JSON.stringify(input) },
    );
  },

  /**
   * Finalize a deployment: once every missing blob is uploaded, send the FULL
   * manifest + the whole-deploy digest. The API re-hashes each stored blob and
   * re-derives the digest server-side before creating the immutable version (201).
   */
  finalizeDeployment(
    siteId: string,
    input: FinalizeDeploymentInput,
  ): Promise<Version> {
    return apiFetch<Version>(`/v1/sites/${siteId}/deployments`, {
      method: "POST",
      body: JSON.stringify(input),
    });
  },

  // ---- Phase 2: access control, sharing policy, members, domains ----------

  /** List the caller org's members (Better Auth roles, RLS/org-scoped). */
  async listMembers(): Promise<Member[]> {
    const body = (await apiGet("/v1/members")) as { members?: Member[] };
    return body.members ?? [];
  },

  /**
   * Logical storage usage per user for the caller org (the members-page usage
   * column). Each user's total is the sum of their sites' current-version sizes;
   * NOT deduplicated. Users with no sites are omitted (treat as 0).
   */
  async storageUsage(): Promise<UserStorage[]> {
    const body = (await apiGet("/v1/storage")) as { users?: UserStorage[] };
    return body.users ?? [];
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
    const body = (await apiGet(`/v1/sites/${siteId}/allowlist`)) as {
      allowlist?: AllowlistEntry[];
    };
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
    const body = (await apiGet(`/v1/sites/${siteId}/domains`)) as {
      domains?: Domain[];
    };
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

  /** Remove a custom domain (admin/owner). 204 No Content on success. */
  async deleteDomain(domainId: string): Promise<void> {
    await apiFetch<void>(`/v1/domains/${domainId}`, { method: "DELETE" });
  },

  /**
   * Read the org's sharing policy (the live allow_external_sharing value) so the UI
   * can render the toggle in its true state instead of a hardcoded default (H10).
   * Any member may read it.
   */
  getOrgPolicy(): Promise<{ allow_external_sharing: boolean; mcp_enabled: boolean }> {
    return apiGet("/v1/orgs/policy") as Promise<{
      allow_external_sharing: boolean;
      mcp_enabled: boolean;
    }>;
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
   * Toggle whether the Dropway MCP server may serve this org (owner/admin only →
   * 403). The MCP resource server re-checks the flag per request, so a disable
   * takes effect immediately even for already-issued OAuth tokens.
   */
  setMcpEnabled(enabled: boolean): Promise<{ mcp_enabled: boolean }> {
    return apiFetch<{ mcp_enabled: boolean }>("/v1/orgs/mcp", {
      method: "PATCH",
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
   * Mint an anonymous edge token for a password-protected site. JWT-FREE, the
   * password is the only credential. 401 → wrong password / unknown host.
   */
  authzPassword(input: { host: string; password: string }): Promise<EdgeToken> {
    return apiFetchPublic<EdgeToken>("/v1/authz/password", {
      method: "POST",
      body: JSON.stringify(input),
    });
  },

  // ---- Phase 3: billing (CLOUD-ONLY). 404 on the self-host build. ------

  /**
   * Read the org's current plan (any authenticated member). plan_tier is read
   * from app.org_meta (authoritative) and is mirrored from the signed Stripe
   * webhook, NOT from any browser redirect. Drives the plan banner + CTAs.
   * On the OSS build this 404s; callers treat that as "no billing".
   */
  getBilling(): Promise<BillingPlan> {
    return apiGet("/v1/billing") as Promise<BillingPlan>;
  },

  /**
   * Start a Stripe Checkout session for {target_tier} (owner/admin → 403
   * otherwise). Returns the Stripe-hosted checkout_url to redirect the user to.
   * The success redirect grants NOTHING, only the webhook flips plan_tier.
   */
  createCheckout(input: {
    target_tier: CheckoutTier;
    seats?: number;
    local_currency?: boolean;
  }): Promise<CheckoutResult> {
    return apiFetch<CheckoutResult>("/v1/billing/checkout", {
      method: "POST",
      body: JSON.stringify(input),
    });
  },

  /**
   * Open the Stripe Billing Portal for the org's existing Customer (owner/admin
   * → 403). Returns portal_url; 409 if the org has no Stripe customer yet (the
   * caller should run Checkout first).
   */
  createPortal(): Promise<PortalResult> {
    return apiFetch<PortalResult>("/v1/billing/portal", { method: "POST" });
  },

  // ---- Phase 4: audit log + hard revocation --------------------------------
  //
  // These hit endpoints that may not exist yet on every build (see the type
  // note above). A 404 is mapped to ApiError(status=404) and the server loaders
  // (lib/audit.ts) treat that as "feature not available", degrading the UI
  // instead of crashing, exactly like the billing 404 → "no billing" path.

  /**
   * List the caller org's recent audit events (owner/admin only → 403). The Go
   * API reads app.audit_log RLS-scoped to the active org and returns newest
   * first. `limit`/`offset` are best-effort pagination; the API may instead
   * return a `next_cursor`.
   *
   * TODO(phase4): replace the manual querystring + shape once /v1/audit is in
   * openapi.yaml (operationId `listAudit`).
   */
  async listAudit(params: { limit?: number; offset?: number } = {}): Promise<AuditPage> {
    const q = new URLSearchParams();
    if (params.limit != null) q.set("limit", String(params.limit));
    if (params.offset != null) q.set("offset", String(params.offset));
    const qs = q.toString();
    const body = (await apiGet(`/v1/audit${qs ? `?${qs}` : ""}`)) as {
      events?: AuditEvent[];
      total?: number;
      next_cursor?: string | null;
    };
    return {
      events: body.events ?? [],
      total: body.total,
      next_cursor: body.next_cursor ?? null,
    };
  },

  /**
   * Hard-revoke a subject's edge tokens by bumping the KV denylist `min_iat`
   * (the REVOCATION DENYLIST CONTRACT). owner/admin only → 403. Used by the
   * members "sign out everywhere" affordance:
   *   - kind="user" → revoke one member's content access immediately
   *   - kind="org"  → org-wide kill switch (sign everyone out everywhere)
   * Idempotent server-side (max of existing/new min_iat); a stale denylist only
   * fails closed (extra re-auth), never opens access.
   *
   * TODO(phase4): replace path/shape once /v1/orgs/revoke-access (or the Go
   * agent's chosen route) lands in openapi.yaml.
   */
  revokeAccess(input: { kind: "user" | "org" | "site"; id: string }): Promise<RevokeResult> {
    return apiFetch<RevokeResult>("/v1/orgs/revoke-access", {
      method: "POST",
      body: JSON.stringify(input),
    });
  },

  /**
   * Members-cap preflight (H8): the invite flow calls this BEFORE inviting. The Go
   * API answers per its (OSS-unlimited or cloud-per-tier) policy, resolving with
   * `{allowed:true}` when the org has room, or throwing an ApiError with status 402
   * (the quota-exceeded upgrade body) when it is at/over its member cap. Keeping the
   * cap decision in the Go API preserves the open-core boundary (the cloud caps
   * never ship in the dashboard build).
   */
  preflightMembers(): Promise<{ allowed: boolean }> {
    return apiGet("/v1/members/preflight") as Promise<{ allowed: boolean }>;
  },

  /**
   * Record a `member.invite` audit entry after Better Auth creates an org
   * invitation (the dashboard owns the invite; the Go API owns the audit trail).
   * admin/owner only → 403. Best-effort: the invitation already exists, so the
   * caller treats a failure (or a 404 on an older API build) as non-fatal.
   */
  recordMemberInvite(input: { email: string; role: string }): Promise<{ recorded: boolean }> {
    return apiFetch<{ recorded: boolean }>("/v1/members/invites", {
      method: "POST",
      body: JSON.stringify(input),
    });
  },

  /**
   * Record a `member.join` audit entry after the caller accepts an invitation and
   * joins the org. Call it only once the JOINED org is the active org (after
   * setActive), so the Go API scopes the row (RLS + actor) to the org they joined.
   * Any member may record their OWN join. Best-effort, like recordMemberInvite.
   */
  recordMemberJoin(): Promise<{ recorded: boolean }> {
    return apiFetch<{ recorded: boolean }>("/v1/members/joined", {
      method: "POST",
      body: JSON.stringify({}),
    });
  },

  // ---- Org-wide skill sharing ----------------------------------------------

  /** List/search the org's shared skills (q, folder slug, presets-only). */
  async listSkills(opts: { q?: string; folder?: string; presets?: boolean } = {}): Promise<Skill[]> {
    const params = new URLSearchParams();
    if (opts.q) params.set("q", opts.q);
    if (opts.folder) params.set("folder", opts.folder);
    if (opts.presets) params.set("presets", "true");
    const qs = params.toString();
    const body = (await apiGet(`/v1/skills${qs ? `?${qs}` : ""}`)) as { skills?: Skill[] };
    return body.skills ?? [];
  },

  /** Get one skill by id (404 → ApiError with status 404). */
  async getSkill(id: string): Promise<Skill> {
    const body = (await apiGet(`/v1/skills/${id}`)) as { skill?: Skill };
    return body.skill ?? {};
  },

  /** Register a skill (metadata; content arrives via the upload flow). */
  async createSkill(input: { slug: string; title?: string; folders?: string[] }): Promise<Skill> {
    const body = await apiFetch<{ skill?: Skill }>("/v1/skills", {
      method: "POST",
      body: JSON.stringify(input),
    });
    return body.skill ?? {};
  },

  /** Delete a skill (owner or org admin). */
  deleteSkill(id: string): Promise<{ deleted?: boolean }> {
    return apiFetch<{ deleted?: boolean }>(`/v1/skills/${id}`, { method: "DELETE" });
  },

  /** Replace a skill's folder memberships (owner or admin). */
  async setSkillFolders(id: string, folders: string[]): Promise<Skill> {
    const body = await apiFetch<{ skill?: Skill }>(`/v1/skills/${id}/folders`, {
      method: "PUT",
      body: JSON.stringify({ folders }),
    });
    return body.skill ?? {};
  },

  /** Skill upload step 1: validate + missing blobs + presigned PUT URLs. */
  prepareSkillUpload(id: string, manifest: ManifestFile[]): Promise<PrepareDeploymentResult> {
    return apiFetch<PrepareDeploymentResult>(`/v1/skills/${id}/uploads/prepare`, {
      method: "POST",
      body: JSON.stringify({ manifest }),
    });
  },

  /** Skill upload step 3: finalize (server-verifies; finalize publishes). */
  finalizeSkillUpload(
    id: string,
    manifest: ManifestFile[],
    digest: string,
  ): Promise<SkillUploadResult> {
    return apiFetch<SkillUploadResult>(`/v1/skills/${id}/uploads`, {
      method: "POST",
      body: JSON.stringify({ manifest, digest }),
    });
  },

  /** Download one skill's files inline (utf8 / base64). */
  downloadSkill(id: string): Promise<SkillDownload> {
    return apiFetch<SkillDownload>(`/v1/skills/${id}/download`);
  },

  /** Bulk-download every skill in a folder (truncated stubs past the budget). */
  downloadSkillFolder(id: string): Promise<SkillFolderDownload> {
    return apiFetch<SkillFolderDownload>(`/v1/skill-folders/${id}/download`);
  },

  /** The org's skill folders (with item counts). */
  async listSkillFolders(): Promise<SkillFolder[]> {
    const body = (await apiGet("/v1/skill-folders")) as { folders?: SkillFolder[] };
    return body.folders ?? [];
  },

  /** Create a skill folder (admin/owner). */
  async createSkillFolder(input: { slug: string; title?: string }): Promise<SkillFolder> {
    const body = await apiFetch<{ folder?: SkillFolder }>("/v1/skill-folders", {
      method: "POST",
      body: JSON.stringify(input),
    });
    return body.folder ?? {};
  },

  /** Retitle a folder (admin/owner; the slug is stable). */
  async renameSkillFolder(id: string, title: string): Promise<SkillFolder> {
    const body = await apiFetch<{ folder?: SkillFolder }>(`/v1/skill-folders/${id}`, {
      method: "PATCH",
      body: JSON.stringify({ title }),
    });
    return body.folder ?? {};
  },

  /** Delete a folder (admin/owner). Its skills survive. */
  deleteSkillFolder(id: string): Promise<{ deleted?: boolean }> {
    return apiFetch<{ deleted?: boolean }>(`/v1/skill-folders/${id}`, { method: "DELETE" });
  },

  /** Add a skill to a folder (admin any + preset flag; owners their own skill). */
  addSkillFolderItem(
    folderId: string,
    input: { skill_id: string; is_preset?: boolean },
  ): Promise<{ added?: boolean }> {
    return apiFetch<{ added?: boolean }>(`/v1/skill-folders/${folderId}/items`, {
      method: "POST",
      body: JSON.stringify(input),
    });
  },

  /** Remove a skill from a folder (admin or the skill's owner). */
  removeSkillFolderItem(folderId: string, skillId: string): Promise<{ removed?: boolean }> {
    return apiFetch<{ removed?: boolean }>(`/v1/skill-folders/${folderId}/items/${skillId}`, {
      method: "DELETE",
    });
  },

  /** Flip a folder membership's preset flag (admin/owner). */
  setSkillFolderItemPreset(
    folderId: string,
    skillId: string,
    isPreset: boolean,
  ): Promise<{ is_preset?: boolean }> {
    return apiFetch<{ is_preset?: boolean }>(`/v1/skill-folders/${folderId}/items/${skillId}`, {
      method: "PATCH",
      body: JSON.stringify({ is_preset: isPreset }),
    });
  },
};
