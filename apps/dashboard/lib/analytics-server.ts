import "server-only";

import { PostHog } from "posthog-node";

import { buildEventProperties } from "@/lib/analytics-shared";
import { appEnvironment, posthogHost, posthogServerKey } from "@/lib/env";

/**
 * Server-side PostHog capture for backend events that must be recorded reliably
 * regardless of the browser: new user signups, sites created, and domains added.
 * These originate in server actions / Better Auth hooks, so a client SDK can't
 * see them. Every event is stamped with `organization` + `environment` (via
 * buildEventProperties) and associated to the org's PostHog group.
 *
 * Disabled cleanly when no key is configured (self-host without PostHog) — the
 * capture helpers become no-ops. Analytics NEVER throws into a user action: all
 * failures are swallowed.
 */

// Lazy singleton. `undefined` = not yet resolved; `null` = resolved to disabled.
let client: PostHog | null | undefined;

function getClient(): PostHog | null {
  if (client !== undefined) return client;
  const key = posthogServerKey();
  if (!key) {
    client = null;
    return client;
  }
  // Every capture below uses the *Immediate methods (captureImmediate /
  // captureExceptionImmediate), which BUILD, ENQUEUE, and SEND the event in a
  // single awaited call — the pattern PostHog documents for serverless (Vercel
  // functions freeze the instant the handler returns). The plain
  // capture()+flush() pattern is racy here: capture() builds the event message
  // asynchronously, so an immediately-following flush() can run before the event
  // is even queued and send nothing (PostHog/posthog-js#2220). flushAt:1 /
  // flushInterval:0 remain as a defensive backstop for any non-immediate path.
  client = new PostHog(key, {
    host: posthogHost(),
    flushAt: 1,
    flushInterval: 0,
  });
  return client;
}

interface ServerEventInput {
  event: string;
  /** The acting user's id — the event's distinct_id. */
  distinctId: string;
  organization?: string | null;
  organizationName?: string | null;
  properties?: Record<string, unknown>;
}

async function captureServerEvent(input: ServerEventInput): Promise<void> {
  const ph = getClient();
  if (!ph) return;
  try {
    // captureImmediate (not capture + flush): sends the event before the awaited
    // call resolves, so it can't be lost to a serverless freeze.
    await ph.captureImmediate({
      distinctId: input.distinctId,
      event: input.event,
      properties: buildEventProperties({
        environment: appEnvironment(),
        organization: input.organization,
        organizationName: input.organizationName,
        properties: input.properties,
      }),
      // Group analytics: tie the event to the org so PostHog can aggregate
      // per-organization. Only when the org is known.
      ...(input.organization
        ? { groups: { organization: input.organization } }
        : {}),
    });
  } catch {
    // Analytics must never break the user action that triggered it.
  }
}

/** A new user finished signing up. Org is usually not set yet (created in
 * onboarding), so `organization` is best-effort. */
export function captureSignup(input: {
  userId: string;
  email?: string | null;
  organization?: string | null;
  method?: string;
}): Promise<void> {
  return captureServerEvent({
    event: "user_signed_up",
    distinctId: input.userId,
    organization: input.organization ?? null,
    properties: { email: input.email ?? undefined, method: input.method },
  });
}

/** A user successfully authenticated: a new session was created. Fires for every
 * sign-in method (email/password, Google, magic link) AND for the initial session
 * Better Auth creates right after signup, so this count is "successful
 * authentications" (logins + signups), not logins alone. Org is best-effort (a
 * brand-new user has none until onboarding). */
export function captureSignInSucceeded(input: {
  userId: string;
  organization?: string | null;
  method?: string;
}): Promise<void> {
  return captureServerEvent({
    event: "sign_in_succeeded",
    distinctId: input.userId,
    organization: input.organization ?? null,
    properties: { method: input.method },
  });
}

/** A sign-in attempt failed at the auth API with an HTTP error status (e.g. 400
 * bad request, 401 invalid credentials, 403 unverified email, 500 server error).
 * There is no authenticated user, so the event is attributed to the system
 * distinct_id (not the attempted email — that would mint a person profile for
 * every typo); the attempted email rides along as a queryable property. */
export function captureSignInFailed(input: {
  status: number;
  code?: string | null;
  method?: string;
  email?: string | null;
}): Promise<void> {
  return captureServerEvent({
    event: "sign_in_failed",
    distinctId: SYSTEM_DISTINCT_ID,
    properties: {
      status: input.status,
      code: input.code ?? undefined,
      method: input.method,
      email: input.email ?? undefined,
    },
  });
}

/** A sign-up attempt failed at the auth API with an HTTP error status (e.g. 422
 * email already in use, 400 weak/invalid password, 500 server error). The
 * counterpart to `user_signed_up` (which only fires on success), so the two
 * together give the sign-up success rate. No user exists, so it's attributed to
 * the system distinct_id; the attempted email rides along as a property. */
export function captureSignUpFailed(input: {
  status: number;
  code?: string | null;
  method?: string;
  email?: string | null;
}): Promise<void> {
  return captureServerEvent({
    event: "sign_up_failed",
    distinctId: SYSTEM_DISTINCT_ID,
    properties: {
      status: input.status,
      code: input.code ?? undefined,
      method: input.method,
      email: input.email ?? undefined,
    },
  });
}

/** A new organization was created (onboarding, or an additional org later).
 * The top of the org-level activation funnel: organization_created →
 * site_created → site_visit, all carrying the same `organization` group. */
export function captureOrganizationCreated(input: {
  userId: string;
  organization: string;
  organizationName?: string | null;
}): Promise<void> {
  return captureServerEvent({
    event: "organization_created",
    distinctId: input.userId,
    organization: input.organization,
    organizationName: input.organizationName ?? null,
  });
}

/** A site was created in the active org. */
export function captureSiteCreated(input: {
  userId: string;
  organization: string;
  siteId?: string;
  slug?: string;
}): Promise<void> {
  return captureServerEvent({
    event: "site_created",
    distinctId: input.userId,
    organization: input.organization,
    properties: { site_id: input.siteId, site_slug: input.slug },
  });
}

/** A custom domain was added to a site in the active org. */
export function captureDomainAdded(input: {
  userId: string;
  organization: string;
  siteId: string;
  domainId?: string;
  hostname?: string;
}): Promise<void> {
  return captureServerEvent({
    event: "domain_added",
    distinctId: input.userId,
    organization: input.organization,
    properties: {
      site_id: input.siteId,
      domain_id: input.domainId,
      hostname: input.hostname,
    },
  });
}

/** Stable distinct_id for infra-level events with no acting user (a pool error can
 * fire from a background idle client, not a request). */
const SYSTEM_DISTINCT_ID = "system";

/**
 * The vendor-neutral server-side error sink: report any caught or unhandled
 * server exception to PostHog Error Tracking. This is the TypeScript analogue of
 * the Go `errtrack.Reporter` seam — every server-side capture path
 * (instrumentation.ts's onRequestError, server actions, Better Auth hooks, the
 * db-capacity check) funnels through here, so swapping PostHog for another vendor
 * is a one-function change.
 *
 * It MUST use captureExceptionImmediate (build + enqueue + send in one awaited
 * call), NOT the plain async captureException. On Vercel the function freezes the
 * instant the handler returns, so a non-immediate capture enqueues
 * asynchronously and can be lost before it flushes (PostHog/posthog-js#2220).
 * Callers should `await` this.
 *
 * Best-effort: no-ops when PostHog is unconfigured (self-host) and never throws
 * into the caller.
 */
export async function captureServerException(input: {
  /** The original error; coerced to an Error so it carries a stack into Error Tracking. */
  error: unknown;
  /** Acting user, when known; defaults to the system distinct_id for infra/background errors. */
  distinctId?: string | null;
  /** Extra queryable properties (route, source, issue tag, …). */
  properties?: Record<string, unknown>;
}): Promise<void> {
  const ph = getClient();
  if (!ph) return;
  try {
    const err =
      input.error instanceof Error ? input.error : new Error(String(input.error));
    await ph.captureExceptionImmediate(err, input.distinctId || SYSTEM_DISTINCT_ID, {
      ...input.properties,
      // environment is platform-owned: spread first so a caller's properties can
      // never clobber the canonical deployment label.
      environment: appEnvironment(),
    });
  } catch {
    // Telemetry must never break the path that produced the error.
  }
}

/**
 * Report a Postgres connection-capacity failure (pooler exhaustion / acquire timeout)
 * to PostHog Error Tracking, so the same condition that logs `[db-capacity]` also raises
 * an alertable issue, grouped by the underlying error with `db_capacity_reason` / `source`
 * as queryable properties an alert can target. Delegates to captureServerException (the
 * Immediate path) so it can't be lost to a serverless freeze.
 */
export async function captureDbCapacityIssue(input: {
  /** Machine-stable reason from connectionCapacityReason (e.g. pooler_session_exhausted). */
  reason: string;
  /** Call site that detected it (e.g. better-auth, firstOrgId, authPool idle client). */
  source: string;
  /** The original error; coerced to an Error so it carries a stack into Error Tracking. */
  error: unknown;
  /** Acting user, when known (firstOrgId path); omitted for background pool errors. */
  distinctId?: string | null;
}): Promise<void> {
  return captureServerException({
    error: input.error,
    distinctId: input.distinctId,
    properties: {
      issue: "db_connection_capacity",
      db_capacity_reason: input.reason,
      source: input.source,
    },
  });
}

// OAuth error codes that indicate a BROKEN handshake (a misconfigured client, a
// server/config bug, a scope/redirect mismatch) rather than an ordinary user
// decision. Only these are raised to Error Tracking as alertable issues; the rest
// (a user clicking "Deny" → access_denied, a not-yet-authenticated authorize →
// login_required) are still recorded as `oauth_error` events for funnel analysis
// but must NOT page anyone — they're expected, not failures.
const ALERTABLE_OAUTH_ERRORS = new Set([
  "invalid_scope",
  "invalid_client",
  "invalid_redirect",
  "invalid_request",
  "unsupported_response_type",
  "unauthorized_client",
  "invalid_grant",
  "server_error",
]);

/**
 * An OAuth 2.1 / MCP-connect authorization attempt failed at the provider
 * endpoints (Dynamic Client Registration, /authorize, /token). These are the
 * errors that BLOCK an AI client (Claude, Cursor, …) or the CLI from connecting —
 * e.g. `invalid_scope`, `invalid_client`, `invalid_redirect` — and, unlike a
 * rejected login, they never surface as a normal app error the user or a server
 * action sees: an /authorize failure is a `?error=` REDIRECT back to the client,
 * and a register/token failure is a 4xx OAuth JSON body. So without this they were
 * invisible — exactly why the `invalid_scope: offline_access` connect regression
 * had no telemetry.
 *
 * We do two things: record an `oauth_error` event (queryable — rate by error code /
 * endpoint / client) AND, for a connection-breaking code (ALERTABLE_OAUTH_ERRORS),
 * raise it to Error Tracking so a spike is alertable, not just a chart. No user is
 * authenticated at this point, so it's attributed to the system distinct_id unless
 * a session is known; client_id / scope / resource ride along as properties.
 *
 * Best-effort and self-swallowing: telemetry must never affect the auth response.
 */
export async function captureOAuthError(input: {
  /** The OAuth endpoint leaf that produced the error: "authorize" | "register" | "token". */
  endpoint: string;
  /** The OAuth error code, e.g. "invalid_scope", "invalid_client", "access_denied". */
  error: string;
  /** The human-readable error_description, when the provider supplied one. */
  errorDescription?: string | null;
  /** HTTP status of the failing response (302 for a redirect error, 4xx for JSON). */
  status?: number;
  /** The requesting client_id, when known. */
  clientId?: string | null;
  /** The requested scope string (the offending scope for invalid_scope), when known. */
  scope?: string | null;
  /** The RFC 8707 resource the token was for (which server), when known. */
  resource?: string | null;
  /** Acting user, when a session exists; defaults to the system distinct_id. */
  distinctId?: string | null;
}): Promise<void> {
  const properties = {
    oauth_endpoint: input.endpoint,
    oauth_error: input.error,
    oauth_error_description: input.errorDescription ?? undefined,
    status: input.status,
    client_id: input.clientId ?? undefined,
    scope: input.scope ?? undefined,
    resource: input.resource ?? undefined,
  };
  await captureServerEvent({
    event: "oauth_error",
    distinctId: input.distinctId || SYSTEM_DISTINCT_ID,
    properties,
  });
  if (ALERTABLE_OAUTH_ERRORS.has(input.error)) {
    // Raise the connection-breaking ones to Error Tracking too, so a broken-connect
    // regression pages instead of merely trending. The message encodes endpoint +
    // code so issues group by failure kind.
    await captureServerException({
      error: new Error(`oauth ${input.endpoint} failed: ${input.error}`),
      distinctId: input.distinctId,
      properties: { issue: "oauth_connect_error", ...properties },
    });
  }
}

/**
 * The provider gracefully NARROWED a client's requested scope: it asked for one or
 * more scopes we don't support, and rather than hard-failing the whole handshake
 * with `invalid_scope` we dropped the unsupported ones and proceeded with the rest
 * (OAuth 2.0 §3.3 permits partially ignoring requested scope). Recorded as an event
 * — NOT an exception: this is the intended graceful path, not a failure. But it's
 * worth seeing: a client repeatedly asking for a scope we drop is a sign we should
 * either add that scope or fix the client, and the trend is the early warning.
 */
export async function captureOAuthScopeDropped(input: {
  /** The OAuth endpoint leaf: "authorize" | "register" | "token". */
  endpoint: string;
  /** The scopes we dropped (unsupported). */
  dropped: string[];
  /** The scopes we kept (supported) and proceeded with. */
  kept: string[];
  /** The requesting client_id, when known. */
  clientId?: string | null;
}): Promise<void> {
  await captureServerEvent({
    event: "oauth_scope_dropped",
    distinctId: SYSTEM_DISTINCT_ID,
    properties: {
      oauth_endpoint: input.endpoint,
      dropped_scopes: input.dropped.join(" "),
      kept_scopes: input.kept.join(" "),
      client_id: input.clientId ?? undefined,
    },
  });
}
