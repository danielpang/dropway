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
  // flushAt:1 / flushInterval:0 → send each event immediately. Combined with an
  // awaited flush() per capture, this survives a serverless function freezing
  // right after the action returns (no buffered, never-sent events).
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
    ph.capture({
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
    await ph.flush();
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
 * Report a Postgres connection-capacity failure (pooler exhaustion / acquire timeout)
 * to PostHog Error Tracking, so the same condition that logs `[db-capacity]` also raises
 * an alertable issue. Sent via captureException (not capture) so it lands in the Error
 * Tracking product grouped by the underlying error, with `db_capacity_reason` / `source`
 * as queryable properties an alert can target. Infra-level and best-effort: no-ops when
 * PostHog is unconfigured (self-host) and never throws into the caller.
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
  const ph = getClient();
  if (!ph) return;
  try {
    const err = input.error instanceof Error ? input.error : new Error(String(input.error));
    ph.captureException(err, input.distinctId || SYSTEM_DISTINCT_ID, {
      issue: "db_connection_capacity",
      db_capacity_reason: input.reason,
      source: input.source,
      environment: appEnvironment(),
    });
    await ph.flush();
  } catch {
    // Telemetry must never break the request/path that hit the capacity error.
  }
}
