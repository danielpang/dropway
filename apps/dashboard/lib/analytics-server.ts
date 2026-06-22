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
